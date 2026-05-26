// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type Definition struct {
	site siteDefinition
}

func New(name string) *Definition {
	return &Definition{site: siteFor(name)}
}

func (d *Definition) Name() string {
	return d.site.Name
}

func (d *Definition) Upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	return upload(ctx, applyTrackerConfig(d.site, req.TrackerConfig), req)
}

func (d *Definition) BuildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, applyTrackerConfig(d.site, req.TrackerConfig), req)
}

func (d *Definition) BuildDescription(ctx context.Context, req trackers.DescriptionRequest) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return trackers.DescriptionResult{}, err
		}
		assets = trackers.DescriptionAssets{}
	}

	description := buildDescription(assets.Description)
	return trackers.DescriptionResult{
		Group:       "azfamily",
		Description: description,
	}, nil
}
