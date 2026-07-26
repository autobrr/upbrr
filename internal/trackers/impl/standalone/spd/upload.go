// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL   = "https://speedapp.io"
	uploadURL = baseURL + "/api/upload"
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	payload       map[string]any
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

type uploadResponse struct {
	Status      bool   `json:"status"`
	Error       bool   `json:"error"`
	DownloadURL string `json:"downloadUrl"`
	Torrent     struct {
		ID any `json:"id"`
	} `json:"torrent"`
}

type channelResult struct {
	ID  any    `json:"id"`
	Tag string `json:"tag"`
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
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: SPD %s", state.blockedReason)
	}

	body, err := json.Marshal(state.payload)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: SPD marshal upload payload: %w", err)
	}
	artifactPath, _ := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "SPD")
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, body, apiKey, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	body []byte,
	apiKey string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD build upload request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", apiKey)

	result, err := commonhttp.ExecuteUpload(&http.Client{Timeout: 30 * time.Second}, httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "SPD",
		SuccessStatus: func(status int) bool { return status == http.StatusOK },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD execute upload: %w", err)
	}

	var decoded uploadResponse
	if err := json.Unmarshal(result.Body, &decoded); err != nil {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "SPD", "upload_failure", result.Preview, ".txt")
		if !result.Success {
			return api.UploadSummary{}, commonhttp.UploadHTTPError("SPD", result.StatusCode, result.Preview)
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD decode response: %s", commonhttp.RedactErrorDetail(err.Error()))
	}
	if result.Success && decoded.Status && !decoded.Error {
		torrentID := strings.TrimSpace(fmt.Sprint(decoded.Torrent.ID))
		if torrentID != "" && artifactPath != "" {
			if dlErr := downloadTrackerTorrent(ctx, baseURL+"/api/torrent/"+torrentID+"/download", apiKey, artifactPath); dlErr != nil {
				trackers.LogRegisteredTorrentUnavailable(req.Logger, "SPD")
				artifactPath = ""
			}
		} else if torrentID != "" {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "SPD")
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "SPD",
				TorrentID:   torrentID,
				TorrentURL:  baseURL + "/browse/" + torrentID,
				DownloadURL: baseURL + "/api/torrent/" + torrentID + "/download",
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "SPD", "upload_failure", result.Preview, ".json")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("SPD", result.StatusCode, result.Preview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	payload := make(map[string]string, len(state.payload))
	for key, value := range state.payload {
		payload[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "SPD",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "spd",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          payload,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: SPD missing api_key")
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
	channelID, blockedReason, questionnaire := resolveChannel(ctx, req)
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: SPD read torrent file: %w", err)
	}
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, fmt.Errorf("trackers: SPD release name: %w", nameErr)
	}
	payload := map[string]any{
		"bdInfo":           trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, req.Meta),
		"coverPhotoUrl":    metautil.FirstNonEmptyTrimmed(tmdbBackdrop(req.Meta), tmdbPoster(req.Meta)),
		"description":      genresText(req.Meta),
		"media_info":       commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"name":             releaseName,
		"nfo":              "",
		"plot":             metautil.FirstNonEmptyTrimmed(req.Meta.EpisodeOverview, tmdbOverview(req.Meta)),
		"poster":           tmdbPoster(req.Meta),
		"technicalDetails": description,
		"screenshots":      combineUniqueScreenshots(assets.MenuImages, assets.Screenshots),
		"type":             resolveCategory(req.Meta),
		"url":              imdbURL(req.Meta),
		"channel":          channelID,
		"file":             base64.StdEncoding.EncodeToString(torrentBytes),
	}
	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		payload:       payload,
		questionnaire: questionnaire,
		blockedReason: blockedReason,
	}, nil
}

func resolveChannel(ctx context.Context, req trackers.PreparationInput) (string, string, *api.TrackerQuestionnaire) {
	input := resolveChannelInput(req.Meta, req.TrackerConfig.Channel)
	if input == "" {
		return "1", "", nil
	}
	if validChannelID(input) {
		return input, "", nil
	}
	id, err := lookupChannelID(ctx, req.TrackerConfig.APIKey, input)
	if err == nil && id != "" {
		return id, "", nil
	}
	return "", "answer the channel questionnaire with a valid channel id or tag", buildChannelQuestionnaire(input)
}

func lookupChannelID(ctx context.Context, apiKey string, input string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/channel?search="+url.QueryEscape(input), nil)
	if err != nil {
		return "", fmt.Errorf("trackers: SPD build channel lookup request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: SPD channel lookup request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var decoded []channelResult
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("trackers: SPD unmarshal channel lookup response: %w", err)
	}
	for _, item := range decoded {
		if strings.EqualFold(strings.TrimSpace(item.Tag), strings.TrimSpace(input)) {
			return strings.TrimSpace(fmt.Sprint(item.ID)), nil
		}
	}
	return "", errors.New("channel not found")
}

func isSeen(seen map[string]struct{}, url string) bool {
	_, ok := seen[url]
	return ok
}

func downloadTrackerTorrent(ctx context.Context, urlValue string, apiKey string, output string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, urlValue, nil)
	if err != nil {
		return fmt.Errorf("trackers: SPD build torrent download request: %w", err)
	}
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	client := &http.Client{Timeout: 20 * time.Second}
	if err := trackers.DownloadRegisteredTorrent(ctx, client, httpReq, output); err != nil {
		return fmt.Errorf("trackers: SPD registered torrent: %w", err)
	}
	return nil
}
