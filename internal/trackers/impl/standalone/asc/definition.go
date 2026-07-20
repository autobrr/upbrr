// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

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
	description := buildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets, req.TrackerConfig.CustomLayout)
	return trackers.DescriptionResult{
		Group:       "asc",
		Description: strings.TrimSpace(description),
	}, nil
}
