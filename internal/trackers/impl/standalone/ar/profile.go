// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"strings"

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
		ReleaseNamePolicy: trackers.WithNonSceneReleaseNameConfirmation(
			trackers.NewReleaseNamePolicy("standalone/ar/v3", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
				name := resolveARName(input.Subject)
				if !input.Subject.Scene && input.RequestedName != nil {
					name = strings.TrimSpace(*input.RequestedName)
				}
				return trackers.ResolvedReleaseNames{Upload: name, Duplicate: resolveARSearchName(input.Subject)}, nil
			}),
		),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		DupePolicy: &trackers.DupePolicy{
			ID:         "ar/duplicate/v2",
			EvidenceID: "ar-uploading-guidelines",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionCodec,
				trackers.DupeDimensionGroup,
			},
			SlotContradictionsRequireManualReview: true,
		},
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
