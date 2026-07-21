// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildUnit3DDescription(
	ctx context.Context,
	_ string,
	meta api.UploadSubject,
	appConfig config.Config,
	trackerConfig config.TrackerConfig,
	logger api.Logger,
	keptDescription string,
	menuImages []api.ScreenshotImage,
	screenshots []api.ScreenshotImage,
	profiles ...SiteProfile,
) (string, error) {
	profile := firstSiteProfile(profiles)
	if profile.BuildDescription != nil {
		return profile.BuildDescription(ctx, meta, appConfig, trackerConfig, logger, keptDescription, menuImages, screenshots)
	}
	description, err := descriptionunit3d.BuildDescription(
		ctx,
		api.NewDescriptionSubject(meta),
		appConfig,
		trackerConfig,
		logger,
		keptDescription,
		menuImages,
		screenshots,
	)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	if profile.FinalizeDescription != nil {
		return profile.FinalizeDescription(description, meta), nil
	}
	return description, nil
}
