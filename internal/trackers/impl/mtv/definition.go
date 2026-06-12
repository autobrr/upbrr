// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	descriptionmtv "github.com/autobrr/upbrr/internal/services/description/mtv"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type Definition struct{}

func New() *Definition {
	return &Definition{}
}

func (d *Definition) Name() string {
	return "MTV"
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

	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
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
		description, err = descriptionmtv.BuildDescription(ctx, req.Meta, req.AppConfig, assets.Description, assets.Screenshots)
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
