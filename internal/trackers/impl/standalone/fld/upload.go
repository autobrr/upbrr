// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://flood.st"
	apiBaseURL = baseURL + "/api/torrents"
	sourceFlag = "FLD"
)

var fldTorrentIDPattern = regexp.MustCompile(`/torrents?/(\d+)`)

type uploadState struct {
	torrentPath   string
	releaseName   string
	description   string
	mediaInfo     string
	fields        map[string]string
	blockedReason string
}

type uploadResponse struct {
	Success    bool   `json:"success"`
	TorrentURL string `json:"torrent_url"`
	Message    string `json:"message"`
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: FLD %s", state.blockedReason)
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "meta_info",
		FileName:  state.releaseName + ".torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "FLD")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	client := httpclient.New(httpclient.DefaultTimeout)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType, client, apiBaseURL, apiKey, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
	client *http.Client,
	apiURL string,
	apiKey string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/upload", bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FLD request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	result, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{Tracker: "FLD"})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FLD execute upload: %w", err)
	}

	var decoded uploadResponse
	if len(result.Body) > 0 {
		if err := json.Unmarshal(result.Body, &decoded); err != nil {
			if !result.Success {
				return api.UploadSummary{}, commonhttp.UploadHTTPError("FLD", result.StatusCode, result.Preview)
			}
			return api.UploadSummary{}, fmt.Errorf("trackers: FLD decode response: %s", commonhttp.RedactErrorDetail(err.Error()))
		}
	}

	if result.Success && decoded.Success && decoded.TorrentURL != "" {
		torrentURL := decoded.TorrentURL
		torrentID := extractTorrentID(torrentURL)
		downloadURL := fmt.Sprintf("%s/download?api_key=%s", strings.TrimRight(torrentURL, "/"), apiKey)

		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "FLD", state.torrentPath, artifactPath, announceURL, sourceFlag,
		)

		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "FLD",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				DownloadURL: downloadURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}

	if _, artifactErr := commonhttp.WriteFailureArtifact(
		req.Meta,
		req.Runtime.DBPath,
		"FLD",
		"upload_failure",
		result.Preview,
		".json",
	); artifactErr != nil && req.Logger != nil {
		req.Logger.Warnf("trackers: FLD failure artifact write failed: %v", artifactErr)
	}

	message := metautil.FirstNonEmptyTrimmed(
		commonhttp.ExtractHTTPErrorDetail(result.Preview),
		commonhttp.RedactErrorDetail(decoded.Message),
		commonhttp.RedactErrorDetail(string(result.Preview)),
		"upload failed",
	)
	return api.UploadSummary{}, fmt.Errorf("trackers: FLD %s", message)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "FLD",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "fld",
		Description:      state.description,
		Endpoint:         apiBaseURL + "/upload",
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "meta_info",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(_ context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: FLD missing api_key")
	}
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)

	mediaInfo, err := resolveMediaInfo(req.Runtime.DBPath, req.Meta)
	if err != nil {
		return uploadState{}, err
	}

	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, fmt.Errorf("trackers: FLD release name: %w", nameErr)
	}

	fields := map[string]string{
		"name":        releaseName,
		"imdb_id":     resolveIMDbID(req.Meta),
		"tmdb_id":     resolveTMDbID(req.Meta),
		"description": description,
		"media_info":  mediaInfo,
		"media_type":  resolveMediaType(req.Meta),
	}

	if req.TrackerConfig.Anon {
		fields["anonymous"] = "checked"
	}

	if edition := strings.TrimSpace(resolveEdition(req.Meta)); edition != "" {
		fields["edition"] = edition
	}

	state := uploadState{
		torrentPath: torrentPath,
		releaseName: releaseName,
		description: description,
		mediaInfo:   mediaInfo,
		fields:      fields,
	}

	if fields["imdb_id"] == "" && fields["tmdb_id"] == "" {
		state.blockedReason = "missing IMDb / TMDb ID"
	}
	if err := validateFLDRequirements(req.Meta); err != nil {
		state.blockedReason = err.Error()
	}

	return state, nil
}

func resolveIMDbID(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func resolveTMDbID(meta api.UploadSubject) string {
	if meta.Identity.TMDBID <= 0 {
		return ""
	}
	if resolveCategory(meta) == "TV" {
		return fmt.Sprintf("tv/%d", meta.Identity.TMDBID)
	}
	return fmt.Sprintf("movie/%d", meta.Identity.TMDBID)
}

func extractTorrentID(torrentURL string) string {
	if matches := fldTorrentIDPattern.FindStringSubmatch(torrentURL); len(matches) > 1 {
		return matches[1]
	}
	trimmed := strings.TrimRight(torrentURL, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		base := trimmed[idx+1:]
		if _, err := strconv.Atoi(base); err == nil {
			return base
		}
	}
	return ""
}
