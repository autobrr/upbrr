// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides SPD tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh SPD tracker definition.
func New() *Definition { return &Definition{} }

// Name returns the stable SPD tracker identifier.
func (Definition) Name() string { return "SPD" }

// BannedGroupPolicy returns SPD's dynamic release-group blacklist settings.
func (Definition) BannedGroupPolicy() *trackers.BannedGroupPolicy {
	return &trackers.BannedGroupPolicy{
		DefaultEndpoint:   "https://speedapp.io/api/torrent/release-group/blacklist",
		EndpointPath:      "/api/torrent/release-group/blacklist",
		RequireAPIKey:     true,
		RawAPIKeyFallback: true,
	}
}

// Prepare builds a fresh intent-scoped SPD tracker plan.
func (d Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (Definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (Definition) prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
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
	return trackers.DescriptionResult{Group: "spd", Description: description}, nil
}
