// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides BHD tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh BHD tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable BHD tracker identifier.
func (d *Definition) Name() string {
	return "BHD"
}

// MetadataPolicy returns BHD metadata requirements.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{
		RequireKnownCategory: true,
		Requirements:         []trackers.MetadataRequirement{{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDB}}},
	}
}

// UploadArtifactPolicy returns BHD torrent personalization settings.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return &trackers.UploadArtifactPolicy{Source: "BHD"}
}

// AudioPolicy returns BHD audio-language restrictions.
func (d *Definition) AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{BlockEnglishOriginalWithForeign: true}
}

// DupePolicy returns BHD-specific duplicate comparison settings.
func (d *Definition) DupePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		MatchAggregateSize:    true,
		NormalizeDDPlusName:   true,
		SDMatchesHD:           true,
		CompareDVDResolution:  true,
		AllowSizeVariance1080: true,
	}
}

// BannedGroups returns BHD's static banned release-group list.
func (d *Definition) BannedGroups() []string {
	return []string{
		"Sicario", "TOMMY", "x0r", "nikt0", "FGT", "d3g", "MeGusta", "YIFY", "tigole", "TEKNO3D",
		"C4K", "RARBG", "4K4U", "EASports", "ReaLHD", "Telly", "AOC", "WKS", "SasukeducK", "CRUCiBLE",
		"iFT", "ProRes", "MezRips", "Flights", "BiTOR", "iVy", "QxR", "SyncUP", "OFT", "TGS",
	}
}

// DataLookupConfigured reports whether BHD metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	entry, _ := bhdConfig(cfg)
	return len(strings.TrimSpace(entry.APIKey)) >= minDataTokenLength && len(strings.TrimSpace(entry.BhdRSSKey)) >= minDataTokenLength
}

// Prepare builds a fresh intent-scoped BHD tracker plan.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareDryRun, d.submit)
}

func (d *Definition) submit(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
	return upload(ctx, req)
}

func (d *Definition) prepareDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	return buildUploadDryRun(ctx, req)
}

func (d *Definition) prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	var err error
	var assets trackers.DescriptionAssets
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
			}
			if req.Logger != nil {
				req.Logger.Warnf("trackers: BHD description assets failed: %v", err)
			}
			assets = trackers.DescriptionAssets{}
		}
	}

	description := buildDescription(req.Meta, req.Runtime.DescriptionConfig(), assets)
	return trackers.DescriptionResult{
		Group:       "bhd",
		Description: description,
	}, nil
}
