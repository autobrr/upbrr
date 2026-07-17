// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
)

// BuildDescription renders the CZTeam user description, preferring caller
// supplied description assets and resolving assets only as a fallback.
func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		resolved, err := trackers.PreparedDescriptionAssets(req.Assets)
		if err == nil {
			assets = resolved
		}
	}
	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)
	return trackers.DescriptionResult{Group: descGroup, Description: description}, nil
}
