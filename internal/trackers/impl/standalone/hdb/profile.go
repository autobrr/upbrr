// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"net/http"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns HDB identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "HDB",
		BaseURL:            hdbBaseURL,
		DescriptionGroup:   "hdb",
		UploadContentMode:  trackers.UploadContentModeDescription,
		ReleaseNamePolicy:  releaseNamePolicy(),
		Rules:              &trackers.RuleSet{MinResolution: "720p"},
		ValidationPolicy:   validationPolicy(),
		PrepareDescription: prepareDescription,
		PrepareUpload: func(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
			return prepareUploadAt(ctx, req, hdbBaseURL, nil)
		},
		NewDuplicateAdapter: func(deps dupe.Dependencies) dupe.Adapter { return newDuplicateAdapterAt(deps, hdbBaseURL) },
		DupePolicy: &trackers.DupePolicy{
			ID:         "hdb/duplicate/v3",
			EvidenceID: "hdb-rules",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionCodec,
				trackers.DupeDimensionGroup,
			},
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
		},
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "HDBits"},
		ArtifactPolicy: &trackers.ArtifactPolicy{
			MaxPieceSizeMiB:     32,
			PieceSizeProfileURL: hdbBaseURL,
		},
		ImageHostPolicy: &trackers.ImageHostPolicy{
			AllowedHosts:         []string{"hdb"},
			OwnedHosts:           []string{"hdb"},
			DisableWithoutRehost: true,
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://tracker.hdbits.org"},
			CommentURLPatterns: []string{hdbBaseURL},
			DetailIDPattern:    "id=(\\d+)",
		},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:          "HDB",
			DisplayName:        "HDB",
			AuthKind:           "passkey_cookies",
			SupportsCookieFile: true,
			RequiresPasskey:    true,
		},
		AuthPolicy: &trackers.AuthPolicy{
			ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
				"passkey_cookie",
				false,
				[]trackers.AuthRequirement{
					trackers.AuthRequirementUsername,
					trackers.AuthRequirementPasskey,
					trackers.AuthRequirementStoredCookie,
				},
			)),
			PasskeyRequiresUsername: true,
			PasskeyRequiresCookie:   true,
		},
		AuthResolver: func(ctx context.Context, cfg config.TrackerConfig, dbPath string, request api.TrackerAuthLoginRequest) error {
			return resolveAuthSessionAt(ctx, cfg, dbPath, request, hdbBaseURL, nil)
		},
	}
}

// Definition extends the shared standalone definition with HDB data lookup and testable endpoints.
type Definition struct {
	*standalone.Definition
	baseURL    string
	httpClient *http.Client
}

// New returns a fresh HDB definition from its tracker-local profile.
func New() *Definition {
	return &Definition{Definition: standalone.MustNew(Profile()), baseURL: hdbBaseURL}
}
