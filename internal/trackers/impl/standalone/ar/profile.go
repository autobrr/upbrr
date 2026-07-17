// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns AR identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "AR",
		BaseURL:             arBaseURL,
		DescriptionGroup:    "ar",
		UploadContentMode:   trackers.UploadContentModeDescription,
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		NewDuplicateAdapter: newDuplicateAdapter,
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{
					trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB, trackers.MetadataFieldTVDB,
				}},
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldPoster}},
			},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: arSourceFlag},
		DupePolicy:            &trackers.DupePolicy{ContainsFilenameMatch: true},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"tracker.alpharatio"}},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:          "AR",
			DisplayName:        "AR",
			AuthKind:           "cookies_login",
			SupportsCookieFile: true,
			SupportsLogin:      true,
			SupportsAutoLogin:  true,
		},
		AuthResolver: func(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
			_, _, err := resolveSession(ctx, cfg, dbPath, nil)
			return err
		},
		AuthStateManager: trackerauth.NewKeyedFileStateManager("AR", arAuthKeyKey, arAuthFile),
	}
}

// New returns a fresh AR definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
