// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package utp

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// buildDescription renders the shared Unit3D description with UTP's image URLs
// remapped so every screenshot links its full-size original.
func buildDescription(
	ctx context.Context,
	meta api.UploadSubject,
	appConfig config.Config,
	trackerConfig config.TrackerConfig,
	logger api.Logger,
	keptDescription string,
	menuImages []api.ScreenshotImage,
	screenshots []api.ScreenshotImage,
) (string, error) {
	description, err := descriptionunit3d.BuildDescription(
		ctx,
		api.NewDescriptionSubject(meta),
		appConfig,
		trackerConfig,
		logger,
		keptDescription,
		swapImageURLs(menuImages),
		swapImageURLs(screenshots),
	)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return description, nil
}

// swapImageURLs remaps each screenshot so the Unit3D description builder
// renders [url=full][img]medium[/img]: the builder uses WebURL for the [url]
// link target and RawURL for the displayed [img], so the full-size RawURL moves
// to WebURL and the medium ImgURL moves to RawURL. Images without a medium
// thumbnail are left unchanged. The input slice is not mutated.
func swapImageURLs(images []api.ScreenshotImage) []api.ScreenshotImage {
	if len(images) == 0 {
		return images
	}
	swapped := make([]api.ScreenshotImage, len(images))
	for i, image := range images {
		full := strings.TrimSpace(image.RawURL)
		medium := strings.TrimSpace(image.ImgURL)
		if full != "" && medium != "" {
			image.WebURL = full
			image.RawURL = medium
		}
		swapped[i] = image
	}
	return swapped
}
