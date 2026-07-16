// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides HDB tracker preparation and optional policy capabilities.
type Definition struct {
	baseURL    string
	httpClient *http.Client
}

// New returns a fresh HDB tracker definition.
func New() *Definition {
	return &Definition{baseURL: hdbBaseURL}
}

// Name returns the stable HDB tracker identifier.
func (d *Definition) Name() string {
	return "HDB"
}

// MetadataPolicy returns HDB metadata requirements.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []trackers.MetadataRequirement{
		{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly}},
		{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly, trackers.MetadataFieldTVDBIDOnly}},
	}}
}

// UploadArtifactPolicy returns HDB torrent personalization settings.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return &trackers.UploadArtifactPolicy{Source: "HDBits"}
}

// ArtifactPolicy returns HDB torrent size constraints.
func (d *Definition) ArtifactPolicy() *trackers.ArtifactPolicy {
	return &trackers.ArtifactPolicy{MaxPieceSizeMiB: 16}
}

// DataLookupConfigured reports whether HDB metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "HDB") {
			return strings.TrimSpace(entry.Username) != "" && strings.TrimSpace(entry.Passkey) != ""
		}
	}
	return false
}

// Prepare builds a fresh intent-scoped HDB tracker plan.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (d *Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return uploadAt(ctx, req, d.baseURL, d.httpClient)
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

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return trackers.DescriptionResult{}, fmt.Errorf("trackers: HDB description assets: %w", err)
		}
		if req.Logger != nil {
			req.Logger.Warnf("trackers: HDB description assets failed: %v", err)
		}
		assets = trackers.DescriptionAssets{}
	}

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description, err = BuildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets.Description, assets.MenuImages, assets.Screenshots)
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
