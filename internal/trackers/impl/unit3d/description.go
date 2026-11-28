// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
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

func ensureUnit3DDVDVOBDescription(description string, meta api.UploadSubject, label string) string {
	return descriptionunit3d.AppendDVDVOBMediaInfoBlock(description, api.NewDescriptionSubject(meta), label)
}

// DescriptionLayout lists the movable groups owned by the Unit3D composer.
func (*Definition) DescriptionLayout() []trackers.DescriptionLayoutItem {
	return trackers.SharedDescriptionLayout()
}

// RenderDescriptionSample uses the site's production composer and formatting
// callbacks with synthetic media already in memory.
func (d *Definition) RenderDescriptionSample(
	ctx context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	site config.TrackerConfig,
) (string, error) {
	return buildUnit3DDescription(ctx, d.profile.Name, sample.Subject, config.Config{Description: defaults}, site, nil,
		sample.Assets.Description, sample.Assets.MenuImages, sample.Assets.Screenshots, d.profile.Site)
}
