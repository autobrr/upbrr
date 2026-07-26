// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, client, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{
		{
			FieldName: "tfile",
			FileName:  state.releaseName + ".torrent",
			Path:      state.torrentPath,
		},
		{
			FieldName: "nfo",
			FileName:  "MEDIAINFO.txt",
			Content:   []byte(commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath))),
		},
	})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "THR")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, client, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	client *http.Client,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: THR build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: THR upload request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if strings.Contains(finalURL, "uploaded=1") {
		torrentID := ""
		if match := idPattern.FindStringSubmatch(finalURL); len(match) == 2 {
			torrentID = match[1]
		}
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "THR", state.torrentPath, artifactPath, announceURL, finalURL, sourceFlag,
		)
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "THR",
			TorrentID:   torrentID,
			TorrentURL:  finalURL,
			TorrentPath: registeredPath,
		}}}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "THR", "upload_failure", bodyBytes, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("THR", resp.StatusCode, bodyBytes)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "THR",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "thr",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "tfile",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, *http.Client, error) {
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
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: THR release name: %w", nameErr)
	}
	fields := map[string]string{
		"name":  releaseName,
		"descr": description,
		"type":  resolveCategory(req.Meta),
		"url":   imdbURL(req.Meta),
		"tube":  youtubeURL(req.Meta),
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
		questionnaire: buildQuestionnaire(req),
	}, client, nil
}
