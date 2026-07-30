// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	assets.Description = trackers.StripDefaultDescriptionSignature(assets.Description)

	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)

	return trackers.DescriptionResult{
		Group:       "ant",
		Description: strings.TrimSpace(description),
	}, nil
}
