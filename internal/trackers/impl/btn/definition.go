// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type Definition struct{}

func New() *Definition {
	return &Definition{}
}

func (d *Definition) Name() string {
	return "BTN"
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

	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}

	description := strings.TrimSpace(assets.Description)
	if description == "" {
		description = strings.TrimSpace(req.Meta.DescriptionOverride)
	}
	if description == "" {
		description = "No description provided."
	}

	return trackers.DescriptionResult{
		Group:       "btn",
		Description: description,
	}, nil
}

func validateBTNRequest(req trackers.UploadRequest) error {
	if !strings.EqualFold(strings.TrimSpace(req.Meta.ExternalIDs.Category), "TV") && !strings.EqualFold(strings.TrimSpace(req.Meta.MediaInfoCategory), "TV") {
		return errors.New("trackers: BTN only supports TV uploads")
	}
	if strings.TrimSpace(config.ResolveBTNAPIToken(req.AppConfig)) == "" {
		return errors.New("trackers: BTN requires trackers.BTN.api_key")
	}
	if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
		return errors.New("trackers: BTN missing username/password")
	}
	return nil
}
