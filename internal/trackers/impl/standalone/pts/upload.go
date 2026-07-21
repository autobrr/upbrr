// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
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

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	state, cookies, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: PTS %s", state.blockedReason)
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file",
		FileName:  "PTS.torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "PTS")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, cookies, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	cookies []*http.Cookie,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
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
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTS read upload response: %w", err)
	}

	location := strings.TrimSpace(resp.Header.Get("Location"))
	torrentID := parseUploadID(location, string(responseBody))
	if (resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther) && torrentID != "" {
		tURL := baseURL + "/details.php?id=" + url.QueryEscape(torrentID)
		if announceURL != "" {
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, tURL, sourceFlag); err != nil {
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

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "PTS", "upload_failure", responsePreview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("PTS", resp.StatusCode, responsePreview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "PTS",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "pts",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, []*http.Cookie, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
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
	cookies, err := loadCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: PTS load cookies: %w", err)
	}
	return state, cookies, nil
}

func buildPayload(meta api.UploadSubject, description string) map[string]string {
	return map[string]string{
		"name":  metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename),
		"url":   imdbURL(meta),
		"descr": description,
		"type":  resolveType(meta),
	}
}

func buildDescription(meta api.UploadSubject, assets trackers.DescriptionAssets) string {
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
	return finalizeDescription(strings.TrimSpace(strings.Join(parts, "\n\n")))
}

func buildQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	if hasMandarin(meta) {
		return nil
	}
	answer := strings.ToLower(strings.TrimSpace(standalone.QuestionnaireAnswers(meta, "PTS")["mandarin_override"]))
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

func validateUpload(meta api.UploadSubject) string {
	if hasMandarin(meta) {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(standalone.QuestionnaireAnswers(meta, "PTS")["mandarin_override"]), "yes") {
		return ""
	}
	return "missing Mandarin audio/subtitles; answer the override questionnaire to continue"
}

func hasMandarin(meta api.UploadSubject) bool {
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
	values, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "PTS", "ptskit.org")
	if err != nil {
		return values, fmt.Errorf("trackers: PTS load cookies: %w", err)
	}
	return values, nil
}

func resolveType(meta api.UploadSubject) string {
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return ""
	}
	if meta.Anime {
		return "407"
	}
	if isTV(meta) {
		return "405"
	}
	return "404"
}

func sanitizeDescription(input string) string {
	return finalizeDescription(input)
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

func imdbURL(meta api.UploadSubject) string {
	if meta.Identity.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.Identity.IMDBID)
}

func isTV(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}
