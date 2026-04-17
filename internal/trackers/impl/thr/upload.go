// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://www.torrenthr.org"
	loginURL   = baseURL + "/takelogin.php"
	uploadURL  = baseURL + "/takeupload.php"
	sourceFlag = "[https://www.torrenthr.org] TorrentHR.org"
)

var (
	subtitleMap = map[string]string{"croatian": "1", "english": "2", "bosnian": "3", "serbian": "4", "slovenian": "5"}
	idPattern   = regexp.MustCompile(`id=(\d+)`)
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, client, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{
		{FieldName: "tfile", FileName: state.releaseName + ".torrent", Path: state.torrentPath},
		{FieldName: "nfo", FileName: "MEDIAINFO.txt", Content: []byte(commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)))},
	})
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: THR upload request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if strings.Contains(finalURL, "uploaded=1") {
		torrentID := ""
		if match := idPattern.FindStringSubmatch(finalURL); len(match) == 2 {
			torrentID = match[1]
		}
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "THR")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, finalURL, sourceFlag); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
			Tracker: "THR", TorrentID: torrentID, TorrentURL: finalURL, DownloadURL: finalURL, TorrentPath: artifactPath,
		}}}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "THR", "upload_failure", bodyBytes, ".html")
	return api.UploadSummary{}, errors.New("trackers: THR upload failed")
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return api.TrackerDryRunEntry{
		Tracker:          "THR",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "thr",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "tfile", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, *http.Client, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(req.Meta, req.TrackerConfig, assets)
	if err != nil {
		return uploadState{}, nil, err
	}
	releaseName := resolveName(req.Meta)
	if override := strings.TrimSpace(questionnaireAnswers(req.Meta)["name_override"]); override != "" {
		releaseName = override
	}
	fields := map[string]string{
		"name":  releaseName,
		"descr": description,
		"type":  resolveCategory(req.Meta),
		"url":   imdbURL(req.Meta),
		"tube":  strings.TrimSpace(req.Meta.ExternalMetadata.TMDB.YouTube),
	}
	for idx, sub := range resolveSubtitles(req.Meta) {
		fields["subs["+strconv.Itoa(idx)+"]"] = sub
	}
	client, err := login(ctx, req.TrackerConfig)
	if err != nil {
		return uploadState{}, nil, err
	}
	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta),
	}, client, nil
}

func login(ctx context.Context, cfg config.TrackerConfig) (*http.Client, error) {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: THR missing username/password")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	form := url.Values{
		"username": {strings.TrimSpace(cfg.Username)},
		"password": {strings.TrimSpace(cfg.Password)},
		"ssl":      {"yes"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("THR login failed with status %d", resp.StatusCode)
	}
	return client, nil
}

func buildDescription(meta api.PreparedMetadata, cfg config.TrackerConfig, assets trackers.DescriptionAssets) (string, error) {
	_ = cfg
	parts := []string{
		"[quote=Info]",
		"Name: " + strings.TrimSpace(firstNonEmpty(meta.Release.Title, meta.ReleaseName)),
		"",
		"Overview: " + strings.TrimSpace(firstNonEmpty(meta.EpisodeOverview, meta.ExternalMetadata.TMDB.Overview)),
		"",
		firstNonEmpty(meta.Release.Resolution, meta.Release.Source) + " / " + strings.TrimSpace(meta.Type),
		"",
		"Category: " + categoryName(meta),
	}
	if tmdb := meta.ExternalIDs.TMDBID; tmdb > 0 {
		parts = append(parts, fmt.Sprintf("TMDB: https://www.themoviedb.org/%s/%d", strings.ToLower(categoryName(meta)), tmdb))
	}
	if imdb := imdbURL(meta); imdb != "" {
		parts = append(parts, "IMDb: "+imdb)
	}
	parts = append(parts, "[/quote]")
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, base)
	}
	for _, image := range assets.Screenshots {
		raw := strings.TrimSpace(firstNonEmpty(image.RawURL, image.ImgURL))
		if raw != "" {
			parts = append(parts, "[img]"+raw+"[/img]")
		}
	}
	parts = append(parts, `[size=2][url=https://www.torrenthr.org/forums.php?action=viewtopic&topicid=8977]upbrr[/url][/size]`)
	return bbcode.FinalizeTrackerDescription("THR", strings.TrimSpace(strings.Join(parts, "\n"))), nil
}

func buildQuestionnaire(meta api.PreparedMetadata) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{
		Tracker: "THR",
		Fields: []api.TrackerQuestionnaireField{{
			Key: "name_override", Label: "Upload Name", Kind: "text", Value: resolveName(meta), Required: true,
		}},
	}
}

func resolveName(meta api.PreparedMetadata) string {
	base := strings.ReplaceAll(firstNonEmpty(meta.ReleaseName, meta.Release.Title, meta.Filename), "DD+", "DDP")
	return regexp.MustCompile(`[^0-9a-zA-Z. '\-\[\]]+`).ReplaceAllString(base, " ")
}

func resolveCategory(meta api.PreparedMetadata) string {
	if containsWord(genresText(meta), "documentary") || containsWord(keywordsText(meta), "documentary") {
		return "12"
	}
	switch categoryName(meta) {
	case "MOVIE":
		if strings.EqualFold(meta.DiscType, "BDMV") {
			return "40"
		}
		if strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.DiscType, "HDDVD") {
			return "14"
		}
		if isSD(meta.Release.Resolution) {
			return "4"
		}
		return "17"
	case "TV":
		if isSD(meta.Release.Resolution) {
			return "7"
		}
		return "34"
	default:
		if meta.Anime {
			return "31"
		}
	}
	return "17"
}

func resolveSubtitles(meta api.PreparedMetadata) []string {
	result := make([]string, 0, len(meta.SubtitleLanguages))
	seen := map[string]struct{}{}
	for _, lang := range meta.SubtitleLanguages {
		id := subtitleMap[strings.ToLower(strings.TrimSpace(lang))]
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func imdbURL(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d/", meta.ExternalIDs.IMDBID)
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["THR"]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func categoryName(meta api.PreparedMetadata) string {
	if isTV(meta) {
		return "TV"
	}
	return "MOVIE"
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}

func genresText(meta api.PreparedMetadata) string {
	return firstNonEmpty(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
}

func keywordsText(meta api.PreparedMetadata) string {
	return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
}
