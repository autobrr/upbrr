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
// New returns a fresh CZT tracker definition through the shared preparation contract.
func New() trackers.Definition  { return definition{} }
func (definition) Name() string { return "CZT" }

func (definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{
		Requirements: []trackers.MetadataRequirement{{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly}}},
	}
}

// Upload submits a CZTeam upload request and returns the persisted registered
// torrent artifact on tracker success.
func (d definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return upload(ctx, req)
}

// BuildUploadDryRun builds the CZTeam multipart payload preview without
// sending it to the tracker.
func (definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

// BuildDescription renders the CZTeam user description, preferring caller
// supplied description assets and resolving assets only as a fallback.
func (definition) prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		resolved, err := trackers.PreparedDescriptionAssets(req.Assets)
		if err == nil {
			assets = resolved
		}
	}
	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)
	return trackers.DescriptionResult{Group: descGroup, Description: description}, nil
}
