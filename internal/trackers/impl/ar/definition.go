// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides AR tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh AR tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable AR tracker identifier.
func (d *Definition) Name() string {
	return "AR"
}

// DupePolicy returns AR-specific duplicate comparison settings.
func (d *Definition) DupePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{ContainsFilenameMatch: true}
}

// Prepare builds a fresh intent-scoped AR tracker plan.
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

	description := buildDescription(req.Meta, req.Runtime.DBPath, assets)
	return trackers.DescriptionResult{
		Group:       "ar",
		Description: strings.TrimSpace(description),
	}, nil
}
