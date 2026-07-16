// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides ASC tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh ASC tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable ASC tracker identifier.
func (d *Definition) Name() string {
	return "ASC"
}

// Prepare builds a fresh intent-scoped ASC tracker plan.
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
	description := buildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets, req.TrackerConfig.CustomLayout)
	return trackers.DescriptionResult{
		Group:       "asc",
		Description: strings.TrimSpace(description),
	}, nil
}
