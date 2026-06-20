// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"context"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type definition struct{}

// New returns the CZTeam tracker definition used by the tracker registry.
func New() trackers.Definition  { return definition{} }
func (definition) Name() string { return "CZT" }

// Upload submits a CZTeam upload request and returns the persisted registered
// torrent artifact on tracker success.
func (definition) Upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	return upload(ctx, req)
}

// BuildUploadDryRun builds the CZTeam multipart payload preview without
// sending it to the tracker.
func (definition) BuildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

// BuildDescription renders the CZTeam user description, preferring caller
// supplied description assets and resolving assets only as a fallback.
func (definition) BuildDescription(ctx context.Context, req trackers.DescriptionRequest) (trackers.DescriptionResult, error) {
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		resolved, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
		if err == nil {
			assets = resolved
		}
	}
	description := buildDescription(trackers.UploadRequest{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		AppConfig:     req.AppConfig,
		Logger:        req.Logger,
		Repo:          req.Repo,
	}, assets)
	return trackers.DescriptionResult{Group: descGroup, Description: description}, nil
}
