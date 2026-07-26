// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path" //nolint:depguard // Extracts tracker response URL path basename.
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://tvchaosuk.com"
	uploadURL  = baseURL + "/api/torrents/upload"
	sourceFlag = "TVCHAOS"
)

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
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if strings.EqualFold(trackers.ResolveRuleResolution(api.NewRuleSubject(req.Meta)), "2160p") {
		return trackers.PreparedOperation{}, errors.New("trackers: TVC disallows UHD uploads")
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: TVC %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "torrent",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "TVC")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType, apiKey, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
	apiKey string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		uploadURL+"?api_token="+url.QueryEscape(apiKey),
		bytes.NewReader(body),
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC build upload request: %s", commonhttp.RedactErrorDetail(err.Error()))
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "Mozilla/5.0")

	result, err := commonhttp.ExecuteUpload(&http.Client{Timeout: 30 * time.Second}, httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "TVC",
		SuccessStatus: func(status int) bool { return status == http.StatusOK },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC execute upload: %w", err)
	}
	if !result.Success {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "TVC", "upload_failure", result.Preview, ".txt")
		return api.UploadSummary{}, commonhttp.UploadHTTPError("TVC", result.StatusCode, result.Preview)
	}

	payload := string(result.Body)
	if strings.Contains(payload, "\n") {
		payload = strings.SplitN(payload, "\n", 2)[1]
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC decode response: %w", err)
	}
	dataURL := strings.TrimSpace(fmt.Sprint(decoded["data"]))
	torrentID := path.Base(strings.TrimRight(dataURL, "/"))

	registeredPath := trackers.PersistReconstructedRegisteredTorrent(
		req.Logger, "TVC", state.torrentPath, artifactPath, announceURL, dataURL, sourceFlag,
	)
	return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
		Tracker:     "TVC",
		TorrentID:   torrentID,
		TorrentURL:  dataURL,
		TorrentPath: registeredPath,
	}}}, nil
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "TVC",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "tvc",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(_ context.Context, req trackers.PreparationInput) (uploadState, error) {
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return uploadState{}, nameFailure
	}
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: TVC missing api_key")
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
	description := buildDescription(req.Meta, req.TrackerConfig, assets)
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, fmt.Errorf("trackers: TVC release name: %w", nameErr)
	}

	fields := map[string]string{
		"name":             releaseName,
		"description":      description,
		"mediainfo":        commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"bdinfo":           "",
		"category_id":      resolveCategory(req.Meta),
		"type":             resolveResolution(req.Meta),
		"tmdb":             strconv.Itoa(req.Meta.Identity.TMDBID),
		"imdb":             strconv.Itoa(req.Meta.Identity.IMDBID),
		"mal":              strconv.Itoa(req.Meta.Identity.MALID),
		"igdb":             "0",
		"anonymous":        boolNum(req.TrackerConfig.Anon),
		"stream":           boolNum(req.Meta.StreamOptimized > 0),
		"sd":               boolNum(isSD(req.Meta.Release.Resolution)),
		"keywords":         keywordsText(req.Meta),
		"personal_release": boolNum(req.Meta.PersonalRelease),
		"internal":         "0",
		"featured":         "0",
		"free":             "0",
		"doubleup":         "0",
		"sticky":           "0",
	}
	if isTV(req.Meta) {
		if isTVCategory(req.Meta) {
			fields["tvdb"] = strconv.Itoa(req.Meta.Identity.TVDBID)
		}
		fields["season_number"] = strconv.Itoa(maxInt(req.Meta.SeasonInt, 0))
		fields["episode_number"] = strconv.Itoa(maxInt(req.Meta.EpisodeInt, 0))
	}

	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		questionnaire: buildQuestionnaire(req),
		blockedReason: validateUpload(req.TrackerConfig, assets),
	}, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
