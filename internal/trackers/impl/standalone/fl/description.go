// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"

	"context"
)

func buildDescription(assets trackers.DescriptionAssets) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	return finalizeDescription(strings.TrimSpace(assets.Description))
}

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	return trackers.DescriptionResult{Group: "fl", Description: buildDescription(assets)}, nil
}
