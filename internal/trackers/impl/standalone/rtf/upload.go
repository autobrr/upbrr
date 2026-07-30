// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

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
	defaultBaseURL = "https://retroflix.club"
	sourceFlag     = "sunshine"
)

var newHTTPClient = func() *http.Client { return &http.Client{Timeout: 40 * time.Second} }

type uploadResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Torrent struct {
		ID any `json:"id"`
	} `json:"torrent"`
}

type uploadState struct {
	torrentPath   string
	releaseName   string
	description   string
	payload       map[string]any
	blockedReason string
}

func uploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (api.UploadSummary, error) {
	req.Intent = trackers.PreparationIntentUpload
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return api.UploadSummary{}, nameFailure
	}
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, baseURL)
	})
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	return prepareUploadAt(ctx, req, defaultBaseURL)
}

func prepareUploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	uploadURL, err := joinURL(baseURL, "/api/upload")
	if err != nil {
		return trackers.PreparedOperation{}, err
	}

	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview, err := buildUploadPreview(state, baseURL)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: RTF %s", state.blockedReason)
	}

	apiKey, err := resolveAPIKey(ctx, req, baseURL)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}

	body, err := json.Marshal(state.payload)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: RTF marshal upload payload: %w", err)
	}
	torrentURL, err := joinURL(baseURL, "/browse/t/")
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	downloadURL, err := joinURL(baseURL, "/api/torrent/")
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "RTF")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(
			submitCtx, req, state, uploadURL, body, apiKey, torrentURL, downloadURL, announceURL, artifactPath,
		)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	uploadURL string,
	body []byte,
	apiKey string,
	torrentURL string,
	downloadURL string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF create upload request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", apiKey)

	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)

	var decoded uploadResponse
	_ = json.Unmarshal(responseBody, &decoded)
	if resp.StatusCode == http.StatusCreated && !decoded.Error {
		id := strings.TrimSpace(fmt.Sprint(decoded.Torrent.ID))
		if id == "" {
			return api.UploadSummary{}, errors.New("trackers: RTF upload succeeded but torrent id missing")
		}
		tURL := torrentURL + id
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "RTF", state.torrentPath, artifactPath, announceURL, tURL, sourceFlag,
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "RTF",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: downloadURL + id + "/download",
				TorrentPath: registeredPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "RTF", "upload_failure", responseBody, ".json")
	return api.UploadSummary{}, fmt.Errorf(
		"trackers: RTF %s",
		metautil.FirstNonEmptyTrimmed(
			commonhttp.ExtractHTTPErrorDetail(responseBody),
			commonhttp.RedactErrorDetail(decoded.Message),
			fmt.Sprintf("upload failed with status %d", resp.StatusCode),
		),
	)
}

func buildUploadPreview(state uploadState, baseURL string) (api.TrackerDryRunEntry, error) {
	payload := make(map[string]string, len(state.payload))
	for key, value := range state.payload {
		payload[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	endpoint, err := joinURL(baseURL, "/api/upload")
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "RTF",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "rtf",
		Description:      state.description,
		Endpoint:         endpoint,
		Payload:          payload,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}), nil
}

func prepareUploadState(_ context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" &&
		(strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "") {
		return uploadState{}, errors.New("trackers: RTF missing api_key or username/password")
	}
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	var assets trackers.DescriptionAssets
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	description := buildDescription(assets)
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: RTF read torrent file: %w", err)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: RTF reviewed upload name: %w", err)
	}
	payload := map[string]any{
		"name":        releaseName,
		"description": description,
		"mediaInfo":   commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"nfo":         "",
		"url":         imdbURL(req.Meta),
		"descr":       description,
		"poster":      resolvePoster(req.Meta),
		"type":        resolveType(req.Meta),
		"screenshots": screenshots(assets.Screenshots),
		"isAnonymous": req.TrackerConfig.Anon,
		"file":        base64.StdEncoding.EncodeToString(torrentBytes),
	}
	return uploadState{
		torrentPath:   torrentPath,
		releaseName:   releaseName,
		description:   description,
		payload:       payload,
		blockedReason: "",
	}, nil
}

// joinURL resolves path against a profile-owned base URL.
func joinURL(baseURL string, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("trackers: RTF invalid base URL %q", baseURL)
	}
	ref, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return "", fmt.Errorf("trackers: RTF invalid URL path %q: %w", path, err)
	}
	return parsed.ResolveReference(ref).String(), nil
}
