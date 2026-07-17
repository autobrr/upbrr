// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
)

// Definition provides one AZ-family tracker profile through the shared contracts.
type Definition struct {
	site siteDefinition
}

// New returns an AZ-family definition for the requested registered profile name.
func New(name string) *Definition {
	return &Definition{site: siteFor(name)}
}

// Name returns the stable tracker identifier for this AZ-family profile.
func (d *Definition) Name() string {
	return d.site.Name
}

// TrackerFamily identifies the definition as AZ-family-backed.
func (d *Definition) TrackerFamily() trackers.Family { return trackers.FamilyAZFamily }

// DefaultBaseURL returns the profile-owned tracker endpoint.
func (d *Definition) DefaultBaseURL() string { return d.site.BaseURL }

// TorrentIdentityPolicy returns this site's tracker announce identity patterns.
func (d *Definition) TorrentIdentityPolicy() *trackers.TorrentIdentityPolicy {
	return &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{d.site.DefaultAnnounceURL}}
}

// UploadArtifactPolicy returns torrent personalization for this AZ-family profile.
func (d *Definition) UploadArtifactPolicy() *trackers.UploadArtifactPolicy {
	return &trackers.UploadArtifactPolicy{
		Source:          d.site.SourceFlag,
		DefaultAnnounce: d.site.DefaultAnnounceURL,
	}
}

// MetadataPolicy returns metadata requirements for this AZ-family profile.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	return &trackers.TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []trackers.MetadataRequirement{
		{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly, trackers.MetadataFieldIMDBIDOnly}},
		{
			Scope: trackers.MetadataScopeTV,
			AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly, trackers.MetadataFieldIMDBIDOnly, trackers.MetadataFieldTVDBIDOnly},
		},
	}}
}

// BannedGroups returns the static banned release-group list for this AZ-family profile.
func (d *Definition) BannedGroups() []string {
	if d.site.Name != "PHD" {
		return nil
	}
	return []string{
		"RARBG",
		"STUTTERSHIT",
		"LiGaS",
		"DDR",
		"Zeus",
		"TBS",
		"SWTYBLZ",
		"EASports",
		"C4K",
		"d3g",
		"MeGusta",
		"YTS",
		"YIFY",
		"Tigole",
		"x0r",
		"nikt0",
		"NhaNc3",
		"PRoDJi",
		"RDN",
		"SANTi",
		"FaNGDiNG0",
		"FRDS",
		"HD2DVD",
		"HDTime",
		"iPlanet",
		"KiNGDOM",
		"Leffe",
		"4K4U",
		"Xiaomi",
		"VisionXpert",
		"WKS",
	}
}

// Prepare builds a fresh intent-scoped tracker plan for this AZ-family profile.
func (d *Definition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareUpload)
}

func (d *Definition) prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	return prepareUpload(ctx, applyTrackerConfig(d.site, req.TrackerConfig), req)
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
			return trackers.DescriptionResult{}, fmt.Errorf("trackers: %w", err)
		}
		assets = trackers.DescriptionAssets{}
	}

	description := buildDescription(assets.Description)
	return trackers.DescriptionResult{
		Group:       "azfamily",
		Description: description,
	}, nil
}
