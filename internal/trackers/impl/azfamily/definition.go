// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Definition provides one AZ-family tracker profile through the shared contracts.
type Definition struct {
	site siteDefinition
}

// New returns the CZ or PHD profile for those normalized names and the AZ
// profile for every other value.
func New(name string) *Definition {
	return &Definition{site: siteFor(name)}
}

// Name returns the stable tracker identifier for this AZ-family profile.
func (d *Definition) Name() string {
	return d.site.Name
}

// TrackerFamily identifies the definition as AZ-family-backed.
func (d *Definition) TrackerFamily() trackers.Family { return trackers.FamilyAZFamily }

// ReleaseNamePolicy returns the versioned AZ-family naming policy with TMDB-authoritative movie years.
func (d *Definition) ReleaseNamePolicy() trackers.ReleaseNamePolicyBinding {
	version := "v1"
	if d.site.Name == "AZ" || d.site.Name == "CZ" {
		version = "v2"
	}
	return trackers.WithMovieYearProvider(trackers.SubjectReleaseNameSearchPolicy(
		fmt.Sprintf("azfamily/%s/%s", strings.ToLower(d.site.Name), version),
		func(meta api.UploadSubject, _ config.TrackerConfig) string { return editName(d.site, meta) },
		func(meta api.UploadSubject, _ config.TrackerConfig) string { return resolveSearchName(meta) },
	), api.IdentityProviderTMDB)
}

// UploadContentMode declares the aggregate description workflow shared by AZ-family sites.
func (d *Definition) UploadContentMode() trackers.UploadContentMode {
	return trackers.UploadContentModeDescription
}

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

// DupePolicy returns only source-backed AZ/CZ overlay behavior. PHD has no
// matching saved duplicate-policy snapshot and therefore uses compatibility
// fallback.
func (d *Definition) DupePolicy() *trackers.DupePolicy {
	var policy *trackers.DupePolicy
	switch d.site.Name {
	case "AZ":
		policy = &trackers.DupePolicy{
			ID:         "az/duplicate/v2",
			EvidenceID: "az-upload-rules",
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
			},
		}
	case "CZ":
		policy = &trackers.DupePolicy{
			ID:         "cz/duplicate/v2",
			EvidenceID: "cz-upload-rules",
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionResolution,
			},
		}
	default:
		return nil
	}
	policy.SearchScope = trackers.DupeSearchScope{
		MaxPages: 100,
	}
	return policy
}

// MetadataPolicy returns metadata requirements for this AZ-family profile.
func (d *Definition) MetadataPolicy() *trackers.TrackerMetadataPolicy {
	switch d.site.Name {
	case "AZ":
		return &trackers.TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []trackers.MetadataRequirement{
			strictMetadataRequirement(
				trackers.MetadataScopeMovie,
				trackers.MetadataFieldTMDBIDOnly,
				trackers.MetadataFieldIMDBIDOnly,
			),
			strictMetadataRequirement(trackers.MetadataScopeTV, trackers.MetadataFieldTVDBIDOnly),
			strictMetadataRequirement(trackers.MetadataScopeAny, trackers.MetadataFieldTMDBOriginCountries),
			strictMetadataRequirement(trackers.MetadataScopeTV, trackers.MetadataFieldTVDBTitle),
			strictMetadataRequirement(trackers.MetadataScopeTV, trackers.MetadataFieldTVDBYear),
		}}
	case "CZ":
		return &trackers.TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []trackers.MetadataRequirement{
			strictMetadataRequirement(
				trackers.MetadataScopeMovie,
				trackers.MetadataFieldTMDBIDOnly,
				trackers.MetadataFieldIMDBIDOnly,
			),
			strictMetadataRequirement(
				trackers.MetadataScopeTV,
				trackers.MetadataFieldIMDBIDOnly,
				trackers.MetadataFieldTVDBIDOnly,
			),
			strictMetadataRequirement(trackers.MetadataScopeAny, trackers.MetadataFieldTMDBOriginCountries),
			strictMetadataRequirement(
				trackers.MetadataScopeMovie,
				trackers.MetadataFieldTMDBTitle,
				trackers.MetadataFieldIMDBTitle,
			),
			strictMetadataRequirement(
				trackers.MetadataScopeTV,
				trackers.MetadataFieldIMDBTitle,
				trackers.MetadataFieldTVDBTitle,
			),
		}}
	default:
		return &trackers.TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []trackers.MetadataRequirement{
			strictMetadataRequirement(
				trackers.MetadataScopeMovie,
				trackers.MetadataFieldTMDBIDOnly,
				trackers.MetadataFieldIMDBIDOnly,
			),
			strictMetadataRequirement(
				trackers.MetadataScopeTV,
				trackers.MetadataFieldTMDBIDOnly,
				trackers.MetadataFieldIMDBIDOnly,
				trackers.MetadataFieldTVDBIDOnly,
			),
			strictMetadataRequirement(trackers.MetadataScopeAny, trackers.MetadataFieldTMDBOriginCountries),
		}}
	}
}

func strictMetadataRequirement(scope trackers.MetadataScope, fields ...trackers.MetadataField) trackers.MetadataRequirement {
	return trackers.MetadataRequirement{
		Scope:       scope,
		AnyOf:       fields,
		Disposition: api.RuleDispositionStrict,
	}
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
	var failure *trackers.PreparationFailure
	input, failure = trackers.PrepareInputWithReleaseNamePolicy(input, d.ReleaseNamePolicy())
	if failure != nil {
		return trackers.TrackerPlan{}, failure
	}
	return trackers.PrepareAdapter(ctx, input, d.prepareDescription, d.prepareUpload)
}

func (d *Definition) prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if failures := validateAZConstructibility(d.site, api.NewTrackerValidationSubject(req.Meta, req.Tracker)); len(failures) > 0 {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %s constructibility: %s", d.site.Name, failures[0].Reason)
	}
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

	description := strings.TrimSpace(assets.Description)
	if !assets.Final {
		description = buildDescription(assets.Description)
	}
	return trackers.DescriptionResult{
		Group:       "azfamily",
		Description: description,
	}, nil
}
