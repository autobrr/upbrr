// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"errors"
	"fmt"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/unit3dmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

type Definition struct {
	name string
}

func New(name string) *Definition {
	return &Definition{name: strings.ToUpper(strings.TrimSpace(name))}
}

func (d *Definition) Name() string {
	return d.name
}

func (d *Definition) Upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	if unit3dmeta.IsKnown(d.name) {
		return uploadUnit3D(ctx, req)
	}
	select {
	case <-ctx.Done():
		return api.UploadSummary{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if req.Logger != nil {
		req.Logger.Infof("trackers: %s upload not implemented (unit3d scaffold)", d.name)
	}
	return api.UploadSummary{}, internalerrors.ErrNotImplemented
}

func (d *Definition) BuildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	if unit3dmeta.IsKnown(d.name) {
		return buildUploadDryRunUnit3D(ctx, req)
	}
	select {
	case <-ctx.Done():
		return api.TrackerDryRunEntry{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if req.Logger != nil {
		req.Logger.Infof("trackers: %s dry-run not implemented (unit3d scaffold)", d.name)
	}
	return api.TrackerDryRunEntry{}, internalerrors.ErrNotImplemented
}

func (d *Definition) BuildDescription(ctx context.Context, req trackers.DescriptionRequest) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if req.Logger != nil {
		req.Logger.Debugf("trackers: %s building unit3d description", d.name)
	}
	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, err
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: %s description assets failed: %v", d.name, err)
			}
			assets = trackers.DescriptionAssets{}
		}
	}
	description, err := buildUnit3DDescription(ctx, d.name, req.Meta, req.AppConfig, req.TrackerConfig, req.Logger, assets.Description, assets.Screenshots)
	if err != nil {
		return trackers.DescriptionResult{}, err
	}
	return trackers.DescriptionResult{
		Group:       "unit3d",
		Description: description,
	}, nil
}

func DefaultTrackers() []string {
	return trackers.Unit3DTrackers()
}

func Register(registry *trackers.Registry, trackersList []string) error {
	if registry == nil {
		return nil
	}
	for _, name := range trackersList {
		if err := registry.Register(New(name)); err != nil {
			return err
		}
	}
	return nil
}
