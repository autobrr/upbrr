// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides MTV tracker preparation and optional policy capabilities.
type Definition struct{ baseURL string }

// New returns a fresh MTV tracker definition.
func New() *Definition {
	return &Definition{baseURL: mtvBaseURL}
}

// Name returns the stable MTV tracker identifier.
func (d *Definition) Name() string {
	return "MTV"
}

// DupePolicy returns MTV-specific duplicate comparison settings.
func (d *Definition) DupePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{ContainsFilenameMatch: true, NormalizeMTVName: true}
}

// Prepare builds a fresh intent-scoped MTV tracker plan.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (d *Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return uploadAt(ctx, req, d.baseURL)
}

func (d *Definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRunAt(ctx, req, d.baseURL)
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
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: MTV description assets failed: %v", err)
			}
			assets = trackers.DescriptionAssets{}
		}
	}

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = BuildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets.Description, assets.Screenshots)
		if err != nil {
			return trackers.DescriptionResult{}, fmt.Errorf("trackers: MTV description build: %w", err)
		}
	}

	if strings.TrimSpace(description) == "" && req.Logger != nil {
		req.Logger.Infof("trackers: MTV preparation description empty")
	}

	return trackers.DescriptionResult{
		Group:       "mtv",
		Description: description,
	}, nil
}
