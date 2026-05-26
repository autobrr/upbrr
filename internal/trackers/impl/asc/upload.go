// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var ascIDPattern = regexp.MustCompile(`torrents-details\.php\?id=(\d+)`)

type uploadState struct {
	uploadURL     string
	torrentPath   string
	description   string
	fields        map[string]string
	blockedReason string
	questionnaire *api.TrackerQuestionnaire
	releaseName   string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC %s", state.blockedReason)
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return api.UploadSummary{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		httpReq.AddCookie(cookie)
	}

	client := httpclient.New(httpclient.DefaultTimeout)
	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	torrentID := parseUploadID(finalURL, string(bodyBytes))
	if resp.StatusCode == http.StatusOK && torrentID != "" {
		torrentURL := baseURL + "/torrents-details.php?id=" + url.QueryEscape(torrentID)
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "ASC")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		maybeAutoApprove(ctx, client, cookies, req.TrackerConfig, torrentID, req.Logger)
		maybeSetInternal(ctx, client, cookies, req.TrackerConfig, req.Meta, torrentID, req.Logger)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "ASC",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				DownloadURL: torrentURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	failurePath := ""
	if pathValue, pathErr := resolveFailurePath(req.Meta, req.AppConfig.MainSettings.DBPath); pathErr == nil {
		failurePath = pathValue
		_ = os.WriteFile(failurePath, bodyBytes, 0o600)
	}
	if failurePath != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload failed status=%d url=%s failure=%s", resp.StatusCode, finalURL, failurePath)
	}
	return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload failed status=%d url=%s", resp.StatusCode, finalURL)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req, true)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	status := "ready"
	message := "dry-run payload generated"
	if state.blockedReason != "" {
		status = "blocked"
		message = state.blockedReason
	}

	return api.TrackerDryRunEntry{
		Tracker:          "ASC",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "asc",
		Description:      state.description,
		Endpoint:         state.uploadURL,
		Payload:          state.fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, []*http.Cookie, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}

	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}

	description := buildDescription(ctx, req.Meta, req.AppConfig, assets, req.TrackerConfig.CustomLayout)

	fields, releaseName := buildPayload(req.Meta, req.TrackerConfig, assets, description)
	state := uploadState{
		uploadURL:     resolveUploadURL(req.Meta),
		torrentPath:   torrentPath,
		description:   description,
		fields:        fields,
		releaseName:   releaseName,
		questionnaire: buildQuestionnaire(req.Meta),
	}
	if reason := validatePayload(ctx, req.Meta, fields, req.AppConfig.MainSettings.DBPath); reason != "" {
		state.blockedReason = reason
	}
	if dryRun {
		return state, nil, nil
	}

	cookies, _, err := LoadCookies(ctx, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: ASC load cookies: %w", err)
	}
	return state, cookies, nil
}

func buildPayload(meta api.PreparedMetadata, trackerCfg config.TrackerConfig, assets trackers.DescriptionAssets, description string) (map[string]string, string) {
	answers := questionnaireAnswers(meta)
	releaseName := resolveUploadTitle(meta)
	fields := map[string]string{
		"ano":        strconv.Itoa(resolveYear(meta)),
		"audio":      resolveAudio(meta),
		"capa":       resolvePoster(meta),
		"codecaudio": resolveAudioCodec(meta),
		"codecvideo": resolveVideoCodec(meta),
		"descr":      description,
		"extencao":   resolveContainer(meta),
		"genre":      resolveGenres(meta, answers),
		"imdb":       resolveIMDbIDText(meta),
		"layout":     firstNonEmpty(strings.TrimSpace(trackerCfg.CustomLayout), "2"),
		"legenda":    resolveSubtitle(meta),
		"name":       releaseName,
		"qualidade":  resolveQuality(meta),
		"takeupload": "yes",
		"tresd":      boolFlag(meta.Is3D != ""),
		"tube":       resolveTrailer(meta),
	}
	resolution := resolveResolution(meta)
	fields["largura"] = resolution["width"]
	fields["altura"] = resolution["height"]
	fields["lang"] = resolveLanguage(meta)
	count := 1
	for _, image := range assets.Screenshots {
		if count > 4 {
			break
		}
		raw := strings.TrimSpace(image.RawURL)
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "amigos-share.club") || strings.Contains(lower, "tmdb.org") || strings.Contains(lower, "imdb.com") || strings.Contains(lower, "themoviedb.org") {
			continue
		}
		fields[fmt.Sprintf("screens%d", count)] = raw
		count++
	}
	if meta.Anime {
		fields["type"] = resolveAnimeType(meta)
		fields["idioma"] = resolveAnimeLanguage(meta)
		fields["lang"] = resolveAnimeAudioLanguage(meta)
	}
	return fields, releaseName
}

func buildQuestionnaire(meta api.PreparedMetadata) *api.TrackerQuestionnaire {
	answers := questionnaireAnswers(meta)
	fields := make([]api.TrackerQuestionnaireField, 0, 2)
	if strings.TrimSpace(resolveOverview(meta, answers)) == "" {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key: "overview", Label: "Sinopse", Kind: "textarea", Value: strings.TrimSpace(answers["overview"]), Required: true,
		})
	}
	if strings.TrimSpace(resolveGenres(meta, answers)) == "" {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key: "genre", Label: "Gêneros", Kind: "text", Value: strings.TrimSpace(answers["genre"]), Placeholder: "Drama, Action", Required: true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ASC", Fields: fields}
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["ASC"]
}

func validatePayload(ctx context.Context, meta api.PreparedMetadata, fields map[string]string, dbPath string) string {
	if reason := authProblem(ctx, dbPath); reason != "" {
		return reason
	}
	if strings.TrimSpace(fields["imdb"]) == "" {
		return "missing IMDb ID"
	}
	if strings.TrimSpace(fields["capa"]) == "" {
		return "missing poster URL"
	}
	if strings.TrimSpace(fields["genre"]) == "" {
		return "missing genre"
	}
	if strings.TrimSpace(resolveOverview(meta, questionnaireAnswers(meta))) == "" {
		return "missing overview"
	}
	return ""
}

func resolveUploadURL(meta api.PreparedMetadata) string {
	if meta.Anime {
		return baseURL + "/enviar-anime.php"
	}
	if categoryOf(meta) == "TV" {
		return baseURL + "/enviar-series.php"
	}
	return baseURL + "/enviar-filme.php"
}

func parseUploadID(finalURL string, body string) string {
	if matches := ascIDPattern.FindStringSubmatch(finalURL); len(matches) == 2 {
		return strings.TrimSpace(matches[1])
	}
	if matches := ascIDPattern.FindStringSubmatch(body); len(matches) == 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	file, err := os.Open(strings.TrimSpace(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: ASC open torrent file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", fmt.Errorf("trackers: ASC write multipart field %q: %w", key, err)
		}
	}
	part, err := writer.CreateFormFile("torrent", filepath.Base(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: ASC create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", fmt.Errorf("trackers: ASC copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: ASC close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func maybeAutoApprove(ctx context.Context, client *http.Client, cookies []*http.Cookie, cfg config.TrackerConfig, torrentID string, logger api.Logger) {
	if client == nil {
		logger.Warnf("trackers: ASC auto approval skipped: client is nil")
		return
	}
	if !cfg.UploaderStatus {
		logger.Debugf("trackers: ASC auto approval skipped: uploader status disabled")
		return
	}
	if strings.TrimSpace(torrentID) == "" {
		logger.Warnf("trackers: ASC auto approval skipped: empty torrentID")
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/uploader_app.php?id="+url.QueryEscape(torrentID), nil)
	if err != nil {
		logger.Warnf("trackers: ASC auto approval failed: %v", err)
		return
	}
	req.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil && logger != nil {
		logger.Warnf("trackers: ASC auto approval failed: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
}

func maybeSetInternal(ctx context.Context, client *http.Client, cookies []*http.Cookie, cfg config.TrackerConfig, meta api.PreparedMetadata, torrentID string, logger api.Logger) {
	if client == nil || !cfg.Internal || strings.TrimSpace(torrentID) == "" {
		logger.Debugf("trackers: ASC internal flag skipped: %v", "client is nil or internal is false or torrentID is empty")
		return
	}
	group := strings.TrimPrefix(strings.TrimSpace(meta.Tag), "-")
	if group == "" || !containsFold(cfg.InternalGroups, group) {
		logger.Debugf("trackers: ASC internal flag skipped: %v", "group is empty or not in internal groups")
		return
	}
	values := url.Values{"id": {torrentID}, "internal": {"yes"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/torrents-edit.php?action=doedit", strings.NewReader(values.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil && logger != nil {
		logger.Warnf("trackers: ASC internal flag failed: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
}

func resolveFailurePath(meta api.PreparedMetadata, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, "[ASC]upload_failure.html"), nil
}
