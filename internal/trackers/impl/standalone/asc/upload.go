// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	minScreenshots = 2
	maxScreenshots = 6
	maxImageBytes  = 5 << 20
)

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	torrentFile, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC release name: %w", err)
	}
	assets, _ := trackers.PreparedDescriptionAssets(req.Assets)
	description := buildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets, req.TrackerConfig.CustomLayout)
	mediaInfo := ascUploadMediaInfo(buildMediaInfo(req.Meta, req.Runtime.DBPath))
	coverURL := resolvePoster(req.Meta)
	payload := buildPayload(req.Meta, req.TrackerConfig, description, releaseName, mediaInfo)

	paths := screenshotPaths(assets)
	cookies, cookieErr := loadSessionCookies(ctx, req.Runtime.DBPath)
	if err := ctx.Err(); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC preparation canceled: %w", err)
	}
	reason := validatePayloadFields(req.Meta, payload, len(paths), coverURL)
	if cookieErr != nil || len(cookies) == 0 {
		reason = "import a current ASC session cookie"
	}
	if reason == "" && !validCoverURL(coverURL) {
		reason = "ASC cover must use an HTTPS metadata image host"
	}

	preview := buildUploadPreview(req.Meta, payload, releaseName, description, torrentFile, coverURL, paths, reason)
	if reason != "" {
		return blockedUploadPreview(req, preview, reason)
	}
	torrentBytes, screenshots, err := readUploadFiles(torrentFile, paths, mediaInfo, req.Meta.DiscType)
	if err != nil {
		return blockedUploadPreview(req, preview, err.Error())
	}
	if req.Intent != trackers.PreparationIntentUpload || req.ExecutionMode == api.WorkflowExecutionModeDebug {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	client := httpclient.New(httpclient.DefaultTimeout)
	cover, err := fetchCover(ctx, client, coverURL)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "ASC")
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC registered torrent path: %w", err)
	}
	files := []commonhttp.FileField{
		{
			FieldName: "torrent",
			FileName:  filepath.Base(torrentFile),
			Content:   torrentBytes,
		},
		cover,
	}
	fields := payload.multipartFields(nil)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		summary, err := submitASC(submitCtx, client, cookies, fields, files, screenshots, artifactPath)
		if summary.Uploaded > 0 && len(summary.UploadedTorrents) > 0 && summary.UploadedTorrents[0].TorrentPath == "" {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "ASC")
		}
		return summary, err
	}, nil), nil
}

func blockedUploadPreview(req trackers.PreparationInput, preview api.TrackerDryRunEntry, reason string) (trackers.PreparedOperation, error) {
	if req.Intent == trackers.PreparationIntentUpload && req.ExecutionMode != api.WorkflowExecutionModeDebug {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC %s", reason)
	}
	preview.Status = "blocked"
	preview.Message = reason
	return trackers.NewPreparedOperation(preview, nil, nil), nil
}

func buildUploadPreview(
	meta api.UploadSubject,
	payload uploadPayload,
	name, description, torrentFile, coverURL string,
	paths []string,
	reason string,
) api.TrackerDryRunEntry {
	files := make([]api.TrackerDryRunFile, 0, 2+len(paths))
	files = append(files,
		api.TrackerDryRunFile{
			Field:   "torrent",
			Path:    torrentFile,
			Present: torrentFile != "",
		},
		api.TrackerDryRunFile{
			Field:   "cover",
			Path:    coverURL,
			Present: coverURL != "",
		},
	)
	for _, path := range paths {
		files = append(files, api.TrackerDryRunFile{
			Field:   "screenshot_paths[]",
			Path:    path,
			Present: true,
		})
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "ASC",
		BlockedReason:    reason,
		ReleaseName:      name,
		DescriptionGroup: "asc",
		Description:      description,
		Endpoint:         siteURL + ascUploadPath,
		Payload:          payload.previewFields(),
		Questionnaire:    buildQuestionnaire(meta),
		Files:            files,
	})
}

func readUploadFiles(torrentFile string, paths []string, mediaInfo, discType string) ([]byte, []commonhttp.FileField, error) {
	torrentBytes, err := commonhttp.FileBytes(torrentFile)
	if err != nil {
		return nil, nil, errors.New("cannot read prepared torrent")
	}
	meta, err := metainfo.Load(bytes.NewReader(torrentBytes))
	if err != nil {
		return nil, nil, errors.New("invalid prepared torrent")
	}
	info, err := meta.UnmarshalInfo()
	if err != nil {
		return nil, nil, errors.New("invalid prepared torrent info")
	}
	if err := validateASCVideoNames(info, mediaInfo, discType); err != nil {
		return nil, nil, err
	}

	images := make([]commonhttp.FileField, 0, len(paths))
	for i, path := range paths {
		im, err := readScreenshot(path)
		if err != nil {
			return nil, nil, fmt.Errorf("screenshot %d: %w", i+1, err)
		}
		images = append(images, im)
	}
	return torrentBytes, images, nil
}
