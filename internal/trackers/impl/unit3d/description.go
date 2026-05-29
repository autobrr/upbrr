// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildUnit3DDescription(ctx context.Context, tracker string, meta api.PreparedMetadata, appConfig config.Config, trackerConfig config.TrackerConfig, logger api.Logger, keptDescription string, menuImages []api.ScreenshotImage, screenshots []api.ScreenshotImage) (string, error) {
	if strings.EqualFold(strings.TrimSpace(tracker), "ACM") {
		return buildACMDescription(ctx, meta, appConfig, trackerConfig, logger, keptDescription, menuImages, screenshots)
	}
	description, err := descriptionunit3d.BuildDescription(ctx, meta, appConfig, trackerConfig, logger, keptDescription, menuImages, screenshots)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(tracker), "SHRI") {
		return applySHRIDescriptionNotes(description, meta), nil
	}
	return description, nil
}
