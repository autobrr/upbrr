// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns AR identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "AR",
		BaseURL:            arBaseURL,
		DescriptionGroup:   "ar",
		UploadContentMode:  trackers.UploadContentModeDescription,
		PrepareDescription: prepareDescription,
		PrepareUpload:      prepareUpload,
		ReleaseNamePolicy: trackers.NewReleaseNamePolicy("standalone/ar/v1", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
			name := resolveARName(input.Subject)
			if input.RequestedName != nil {
				name = normalizeARName(*input.RequestedName, input.Subject.Tag)
			}
			return trackers.ResolvedReleaseNames{Upload: name, Duplicate: resolveARSearchName(input.Subject)}, nil
		}),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{
				{
					Scope:       trackers.MetadataScopeMovie,
					AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB},
					Disposition: api.RuleDispositionStrict,
				},
				{
					Scope: trackers.MetadataScopeTV,
					AnyOf: []trackers.MetadataField{
						trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB, trackers.MetadataFieldTVDB,
					},
					Disposition: api.RuleDispositionStrict,
				},
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
		AuthPolicy: &trackers.AuthPolicy{
			ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
				"cookies_or_login",
				false,
				[]trackers.AuthRequirement{trackers.AuthRequirementStoredCookie},
				[]trackers.AuthRequirement{trackers.AuthRequirementCredentialLogin},
			)),
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
