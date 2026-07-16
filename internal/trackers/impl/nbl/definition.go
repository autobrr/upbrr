// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides NBL tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh NBL tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable NBL tracker identifier.
func (d *Definition) Name() string {
	return "NBL"
}

// Prepare builds a fresh intent-scoped NBL tracker plan.
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

	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if req.Logger != nil {
				req.Logger.Errorf(
					"trackers: %s description assets resolution failed source=%s: %v",
					strings.ToUpper(strings.TrimSpace(req.Tracker)),
					strings.TrimSpace(req.Meta.SourcePath),
					err,
				)
			}
			assets = trackers.DescriptionAssets{}
		}
	}

	return trackers.DescriptionResult{
		Group:       "nbl",
		Description: strings.TrimSpace(assets.Description),
	}, nil
}
