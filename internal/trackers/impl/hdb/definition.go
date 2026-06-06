// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	descriptionhdb "github.com/autobrr/upbrr/internal/services/description/hdb"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type Definition struct{}

func New() *Definition {
	return &Definition{}
}

func (d *Definition) Name() string {
	return "HDB"
}

func (d *Definition) Upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (d *Definition) BuildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (d *Definition) BuildDescription(ctx context.Context, req trackers.DescriptionRequest) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := resolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger, req.Assets)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return trackers.DescriptionResult{}, err
		}
		if req.Logger != nil {
			req.Logger.Warnf("trackers: HDB description assets failed: %v", err)
		}
		assets = trackers.DescriptionAssets{}
	}

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = descriptionhdb.BuildDescription(ctx, req.Meta, req.AppConfig, assets.Description, assets.Screenshots)
		if err != nil {
			return trackers.DescriptionResult{}, fmt.Errorf("trackers: HDB description build: %w", err)
		}
	}

	if strings.TrimSpace(description) == "" && req.Logger != nil {
		req.Logger.Infof("trackers: HDB preparation description empty")
	}

	return trackers.DescriptionResult{
		Group:       "hdb",
		Description: description,
	}, nil
}
