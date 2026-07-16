// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides BHDTV tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh BHDTV tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable BHDTV tracker identifier.
func (d *Definition) Name() string {
	return "BHDTV"
}

// Prepare builds a fresh intent-scoped BHDTV tracker plan.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (d *Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (d *Definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (d *Definition) prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}

	description := buildDescription(assets)
	return trackers.DescriptionResult{
		Group:       "bhdtv",
		Description: description,
	}, nil
}

// NewDuplicateAdapter returns BHDTV's explicit manual-check outcome adapter.
func (d *Definition) NewDuplicateAdapter(dupe.Dependencies) dupe.Adapter {
	return bhdtvDuplicateAdapter{}
}

type bhdtvDuplicateAdapter struct{}

func (bhdtvDuplicateAdapter) Search(context.Context, api.DuplicateSubject) dupe.AdapterResult {
	return dupe.NotRun(dupe.NotRunManualCheckRequired, "BHDTV duplicate search requires a manual check", nil)
}
