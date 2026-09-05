// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	baseURL    = "https://digitalcore.club"
	apiBaseURL = baseURL + "/api/v1/torrents"
	sourceFlag = "DigitalCore.club"
)

type uploadState struct {
	torrentPath   string
	releaseName   string
	description   string
	mediaInfo     string
	fields        map[string]string
	blockedReason string
}

type uploadResponse struct {
	ID      any    `json:"id"`
	Message string `json:"message"`
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
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: DC %s", state.blockedReason)
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file",
		FileName:  state.releaseName + ".torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	artifactPath, _ := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "DC")
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	client := httpclient.New(httpclient.DefaultTimeout)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, body, contentType, client, baseURL, apiBaseURL, apiKey, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	body []byte,
	contentType string,
	client *http.Client,
	siteBaseURL string,
	apiURL string,
	apiKey string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/upload", bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("X-Api-Key", apiKey)

	result, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{Tracker: "DC"})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC execute upload: %w", err)
	}
	var decoded uploadResponse
	if len(result.Body) > 0 {
		if err := json.Unmarshal(result.Body, &decoded); err != nil {
			if !result.Success {
				return api.UploadSummary{}, commonhttp.UploadHTTPError("DC", result.StatusCode, result.Preview)
			}
			return api.UploadSummary{}, fmt.Errorf("trackers: DC decode response: %s", commonhttp.RedactErrorDetail(err.Error()))
		}
	}
	torrentID := strings.TrimSpace(fmt.Sprint(decoded.ID))
	if result.Success && torrentID != "" && torrentID != "<nil>" {
		torrentURL := siteBaseURL + "/torrent/" + torrentID + "/"
		downloadURL := apiURL + "/download/" + torrentID
		summary := api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "DC",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				DownloadURL: downloadURL,
			}},
		}
		if artifactPath == "" {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "DC")
			return summary, nil
		}
		downloadRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
		if requestErr != nil {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "DC")
			return summary, nil
		}
		downloadRequest.Header.Set("User-Agent", "upbrr")
		downloadRequest.Header.Set("X-Api-Key", apiKey)
		if downloadErr := trackers.DownloadRegisteredTorrent(ctx, client, downloadRequest, artifactPath); downloadErr != nil {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "DC")
			return summary, nil
		}
		summary.UploadedTorrents[0].TorrentPath = artifactPath
		return summary, nil
	}

	if _, artifactErr := commonhttp.WriteFailureArtifact(
		req.Meta,
		req.Runtime.DBPath,
		"DC",
		"upload_failure",
		result.Preview,
		".json",
	); artifactErr != nil &&
		req.Logger != nil {
		req.Logger.Warnf("trackers: DC failure artifact write failed: %v", artifactErr)
	}
	message := metautil.FirstNonEmptyTrimmed(
		commonhttp.ExtractHTTPErrorDetail(result.Preview),
		commonhttp.RedactErrorDetail(decoded.Message),
		commonhttp.RedactErrorDetail(string(result.Preview)),
		"upload failed",
	)
	return api.UploadSummary{}, fmt.Errorf("trackers: DC %s", message)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "DC",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "dc",
		Description:      state.description,
		Endpoint:         apiBaseURL + "/upload",
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(_ context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: DC missing api_key")
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
	mediaInfo, err := resolveMediaInfo(req, req.Meta)
	if err != nil {
		return uploadState{}, err
	}
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, fmt.Errorf("trackers: DC release name: %w", nameErr)
	}
	fields := map[string]string{
		"category":        strconv.Itoa(resolveCategoryID(req.Meta)),
		"imdbId":          resolveIMDbID(req.Meta),
		"nfo":             description,
		"mediainfo":       mediaInfo,
		"reqid":           "0",
		"section":         "new",
		"frileech":        "1",
		"anonymousUpload": resolveAnon(req),
		"p2p":             "0",
		"unrar":           "1",
	}
	state := uploadState{
		torrentPath: torrentPath,
		releaseName: releaseName,
		description: description,
		mediaInfo:   mediaInfo,
		fields:      fields,
	}
	if strings.TrimSpace(fields["imdbId"]) == "" {
		state.blockedReason = "missing IMDb ID"
	}
	return state, nil
}

func resolveIMDbID(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func resolveAnon(req trackers.PreparationInput) string {
	if req.TrackerConfig.Anon {
		return "1"
	}
	return "0"
}
