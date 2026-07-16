// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides RTF tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh RTF tracker definition.
func New() *Definition { return &Definition{} }

// Name returns the stable RTF tracker identifier.
func (Definition) Name() string { return "RTF" }

// DupePolicy returns RTF-specific duplicate comparison settings.
func (Definition) DupePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{ContainsFilenameMatch: true}
}

// Prepare builds a fresh intent-scoped RTF tracker plan.
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
	var (
		err    error
		assets trackers.DescriptionAssets
	)
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			assets = trackers.DescriptionAssets{}
		}
	}
	description := buildDescription(assets)
	return trackers.DescriptionResult{Group: "rtf", Description: description}, nil
}
