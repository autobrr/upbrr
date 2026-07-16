// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"context"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type definition struct{}

// New returns a fresh TVC tracker definition through the shared preparation contract.
func New() trackers.Definition  { return definition{} }
func (definition) Name() string { return "TVC" }

func (definition) NewDuplicateAdapter(dupe.Dependencies) dupe.Adapter { return tvcDuplicateAdapter{} }

type tvcDuplicateAdapter struct{}

func (tvcDuplicateAdapter) Search(_ context.Context, meta api.DuplicateSubject) dupe.AdapterResult {
	resolution := strings.ToLower(strings.TrimSpace(meta.Release.Resolution))
	if strings.Contains(resolution, "2160") || strings.EqualFold(meta.Type, "REMUX") || strings.TrimSpace(meta.DiscType) != "" {
		return dupe.NotRun(dupe.NotRunUnsupportedContent, "TVC disallows UHD, disc, and remux content", nil)
	}
	return dupe.NotRun(dupe.NotRunManualCheckRequired, "TVC duplicate search requires a manual check", nil)
}

func (d definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (definition) prepareDescription(_ context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, req.TrackerConfig, assets)
	return trackers.DescriptionResult{Group: "tvc", Description: description}, nil
}
