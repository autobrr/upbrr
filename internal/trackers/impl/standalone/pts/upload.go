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
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
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
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "PTS", state.torrentPath, artifactPath, announceURL, sourceFlag,
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "PTS",
				TorrentID:   torrentID,
				TorrentURL:  tURL,
				DownloadURL: baseURL + "/download.php?id=" + url.QueryEscape(torrentID),
				TorrentPath: registeredPath,
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
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, assets)
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: PTS reviewed upload name: %w", err)
	}

	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        buildPayload(req.Meta, description, releaseName),
		questionnaire: buildQuestionnaire(req.Meta),
		blockedReason: validateUpload(req.Meta),
	}
	cookies, err := loadCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: PTS load cookies: %w", err)
	}
	return state, cookies, nil
}

func buildPayload(meta api.UploadSubject, description string, releaseName string) map[string]string {
	return map[string]string{
		"name":  releaseName,
		"url":   imdbURL(meta),
		"descr": description,
		"type":  resolveType(meta),
	}
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
