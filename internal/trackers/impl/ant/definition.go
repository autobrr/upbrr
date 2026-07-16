// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides ANT tracker preparation and optional policy capabilities.
type Definition struct{}

// New returns a fresh ANT tracker definition.
func New() *Definition {
	return &Definition{}
}

// Name returns the stable ANT tracker identifier.
func (d *Definition) Name() string {
	return "ANT"
}

// Rules returns ANT-specific release validation requirements.
func (d *Definition) Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{RequireMovieOnly: true}
}

// ArtifactPolicy returns ANT torrent size constraints.
func (d *Definition) ArtifactPolicy() *trackers.ArtifactPolicy {
	return &trackers.ArtifactPolicy{MaxPieceSizeMiB: 128, MaxTorrentBytes: 250 << 10}
}

// BannedGroups returns ANT's static banned release-group list.
func (d *Definition) BannedGroups() []string {
	groups := slices.Collect(maps.Keys(antBannedReleaseGroups))
	slices.Sort(groups)
	return groups
}

// MetadataPolicy returns ANT metadata requirements.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{
		RequireKnownCategory: true,
		Requirements:         []trackers.MetadataRequirement{{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB}}},
	}
}

// UploadArtifactPolicy returns ANT torrent personalization settings.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return &trackers.UploadArtifactPolicy{Source: "ANT"}
}

// DupePolicy returns ANT-specific duplicate comparison settings.
func (d *Definition) DupePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{DolbyVisionImpliesHDR: true}
}

// AudioPolicy returns ANT audio-language restrictions.
func (d *Definition) AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowedLanguages: []string{"english"}, BlockEnglishOriginalWithForeign: true}
}

// Prepare builds a fresh intent-scoped ANT tracker plan.
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

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	assets.Description = trackers.StripDefaultDescriptionSignature(assets.Description)

	description := buildDescription(trackers.PreparationInput{
		Tracker:       req.Tracker,
		Meta:          req.Meta,
		TrackerConfig: req.TrackerConfig,
		Runtime:       req.Runtime,
		Logger:        req.Logger,
	}, assets)

	return trackers.DescriptionResult{
		Group:       "ant",
		Description: strings.TrimSpace(description),
	}, nil
}
