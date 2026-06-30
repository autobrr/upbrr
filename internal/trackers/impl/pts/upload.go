// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://www.ptskit.org"
	uploadURL  = baseURL + "/takeupload.php"
	sourceFlag = "[www.ptskit.org] PTSKIT"
)

var idPattern = regexp.MustCompile(`download\.php\?id=([^&]+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTS %s", state.blockedReason)
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file",
		FileName:  "PTS.torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	resp, err := (&http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTS upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther, commonhttp.DefaultResponsePreviewBytes)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTS read upload response: %w", err)
	}

	location := strings.TrimSpace(resp.Header.Get("Location"))
	torrentID := parseUploadID(location, string(responseBody))
	if (resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther) && torrentID != "" {
		tURL := baseURL + "/details.php?id=" + url.QueryEscape(torrentID)
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "PTS")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "PTS",
				TorrentID:   torrentID,
				TorrentURL:  tURL,
				DownloadURL: baseURL + "/download.php?id=" + url.QueryEscape(torrentID),
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "PTS", "upload_failure", responsePreview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("PTS", resp.StatusCode, responsePreview)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req)
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
		Tracker:          "PTS",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "pts",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, []*http.Cookie, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, assets)

	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename),
		fields:        buildPayload(req.Meta, description),
		questionnaire: buildQuestionnaire(req.Meta),
		blockedReason: validateUpload(req.Meta),
	}
	cookies, err := loadCookies(ctx, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: PTS load cookies: %w", err)
	}
	return state, cookies, nil
}

func buildPayload(meta api.PreparedMetadata, description string) map[string]string {
	return map[string]string{
		"name":  metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename),
		"url":   imdbURL(meta),
		"descr": description,
		"type":  resolveType(meta),
	}
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) string {
	parts := make([]string, 0, 4)
	if info := commonhttp.ReadOptionalFile(strings.TrimSpace(meta.MediaInfoTextPath)); strings.TrimSpace(info) != "" {
		parts = append(parts, info)
	}
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, sanitizeDescription(base))
	}
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	parts = append(parts, "[right][url=https://github.com/autobrr/upbrr][size=1]upbrr[/size][/url][/right]")
	return bbcode.FinalizeTrackerDescription("PTS", strings.TrimSpace(strings.Join(parts, "\n\n")))
}

func buildQuestionnaire(meta api.PreparedMetadata) *api.TrackerQuestionnaire {
	if hasMandarin(meta) {
		return nil
	}
	answer := strings.ToLower(strings.TrimSpace(questionnaireAnswers(meta)["mandarin_override"]))
	return &api.TrackerQuestionnaire{
		Tracker: "PTS",
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "mandarin_override",
			Label:    "Mandarin Requirement",
			Kind:     "select",
			Options:  []string{"no", "yes"},
			Value:    metautil.FirstNonEmptyTrimmed(answer, "no"),
			Help:     "PTS expects Mandarin audio or subtitles. Choose yes to override and upload anyway.",
			Required: true,
		}},
	}
}

func validateUpload(meta api.PreparedMetadata) string {
	if hasMandarin(meta) {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(questionnaireAnswers(meta)["mandarin_override"]), "yes") {
		return ""
	}
	return "missing Mandarin audio/subtitles; answer the override questionnaire to continue"
}

func hasMandarin(meta api.PreparedMetadata) bool {
	for _, values := range [][]string{meta.AudioLanguages, meta.SubtitleLanguages} {
		for _, value := range values {
			lower := strings.ToLower(strings.TrimSpace(value))
			if strings.Contains(lower, "mandarin") || strings.Contains(lower, "chinese") {
				return true
			}
		}
	}
	return false
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return wrapTrackerResult(cookies.LoadTrackerHTTPCookies(ctx, dbPath, "PTS", "ptskit.org"))
}

func resolveType(meta api.PreparedMetadata) string {
	if meta.Anime {
		return "407"
	}
	if isTV(meta) {
		return "405"
	}
	return "404"
}

func sanitizeDescription(input string) string {
	return bbcode.FinalizeTrackerDescription("PTS", input)
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	lines := []string{"[center][b]Screenshots[/b]"}
	for _, image := range images {
		imgURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL))
		webURL := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.WebURL, imgURL))
		if imgURL == "" || webURL == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[url=%s][img]%s[/img][/url]", webURL, imgURL))
	}
	lines = append(lines, "[/center]")
	return strings.Join(lines, "\n")
}

func parseUploadID(location string, body string) string {
	for _, value := range []string{location, body} {
		match := idPattern.FindStringSubmatch(value)
		if len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func imdbURL(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["PTS"]
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	maps.Copy(out, input)
	return out
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}
