// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
)

func prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)
	return trackers.DescriptionResult{Group: "tl", Description: description}, nil
}
