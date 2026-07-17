// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type preparedUploadContentState string

const (
	preparedUploadContentNotRequired preparedUploadContentState = "not_required"
	preparedUploadContentReady       preparedUploadContentState = "ready"
	preparedUploadContentFailed      preparedUploadContentState = "failed"
)

// preparedUploadContent is the sole tracker-scoped result between shared
// content preparation and tracker adapter invocation.
type preparedUploadContent struct {
	Mode      UploadContentMode
	State     preparedUploadContentState
	Assets    *DescriptionAssets
	ImageHost api.ImageHostFeedback
	Failure   *api.TrackerContentFailure
}

func (s *Service) prepareUploadContent(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	trackerCfg config.TrackerConfig,
	preloaded *preloadedDescriptionAssetData,
	preflight imageHostPreflight,
) preparedUploadContent {
	mode, ok := s.registry.LookupUploadContentMode(tracker)
	if !ok {
		return failedPreparedUploadContent(tracker, UploadContentModeDescription, errors.New("tracker upload content capability is unavailable"))
	}
	if mode == UploadContentModeNone {
		return preparedUploadContent{Mode: mode, State: preparedUploadContentNotRequired}
	}

	resolution, found := preflight[normalizeTrackerName(tracker)]
	var err error
	if !found {
		resolution, err = ensureDescriptionImageHostWithDataAndRegistry(
			ctx,
			tracker,
			meta,
			s.cfg,
			trackerCfg,
			s.repo,
			s.images,
			logging.FromContext(ctx, s.logger),
			s.registry,
			preloaded,
		)
	}
	if err != nil {
		return failedPreparedUploadContent(tracker, mode, err)
	}
	if resolution.blocking {
		message := strings.TrimSpace(resolution.feedback.Message)
		if message == "" {
			message = "image-host requirements could not be met"
		}
		failed := failedPreparedUploadContent(tracker, mode, errors.New(message))
		failed.ImageHost = blockedPreparationImageHostFeedback(resolution.feedback)
		return failed
	}

	var assets DescriptionAssets
	switch mode {
	case UploadContentModeNone:
		return preparedUploadContent{Mode: mode, State: preparedUploadContentNotRequired}
	case UploadContentModeScreenshots:
		assets, err = resolveScreenshotAssets(ctx, tracker, meta, s.repo, logging.FromContext(ctx, s.logger), preloaded, s.registry)
	case UploadContentModeDescription:
		assets, err = resolveDescriptionAssets(ctx, tracker, meta, s.repo, logging.FromContext(ctx, s.logger), preloaded, s.registry)
	default:
		err = errors.New("unsupported tracker upload content mode")
	}
	if err != nil {
		return failedPreparedUploadContent(tracker, mode, err)
	}
	applyResolvedDescriptionScreenshots(ctx, meta, s.repo, preloaded, &assets, resolution.screenshots)
	return preparedUploadContent{
		Mode:      mode,
		State:     preparedUploadContentReady,
		Assets:    &assets,
		ImageHost: resolution.feedback,
	}
}

func failedPreparedUploadContent(tracker string, mode UploadContentMode, err error) preparedUploadContent {
	message := safeTrackerMessage(err)
	return preparedUploadContent{
		Mode:  mode,
		State: preparedUploadContentFailed,
		Failure: &api.TrackerContentFailure{
			Tracker: normalizeTrackerName(tracker),
			Code:    mode.FailureReasonCode(),
			Message: message,
		},
	}
}

func resolveScreenshotAssets(
	ctx context.Context,
	tracker string,
	meta api.UploadSubject,
	repo UploadPersistence,
	logger api.Logger,
	preloaded *preloadedDescriptionAssetData,
	registry *Registry,
) (DescriptionAssets, error) {
	slots, screenshots, err := resolveDescriptionScreenshots(ctx, tracker, meta, repo, logger, preloaded, registry)
	if err != nil {
		return DescriptionAssets{}, err
	}
	menuImages, normalScreenshots := splitDescriptionScreenshots(ctx, meta, repo, preloaded, screenshots)
	return DescriptionAssets{
		Screenshots: normalScreenshots,
		MenuImages:  menuImages,
		Slots:       slots,
	}, nil
}
