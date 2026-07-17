// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns BTN identity, preparation, dupe, auth, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "BTN",
		BaseURL:            btnDefaultBaseURL,
		DescriptionGroup:   "btn",
		PrepareDescription: prepareDescription,
		PrepareUpload: func(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
			return prepareUploadAt(ctx, req, btnDefaultBaseURL)
		},
		NewDuplicateAdapter: newDuplicateAdapter,
		BannedGroups:        bannedGroups(),
		DataPolicy:          &trackers.DataLookupPolicy{DeferWhenCollectingImages: true},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldIMDB, trackers.MetadataFieldTVDB},
			}},
		},
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "BTN", RequireAnnounce: true},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns:       []string{"https://broadcasthe.net", "https://backup.landof.tv", "https://landof.tv", "landof.tv/"},
			CommentURLPatterns:       []string{"https://broadcasthe.net", "https://backup.landof.tv", "https://landof.tv"},
			DetailIDPattern:          "id=(\\d+)",
			InferMatchFromResolvedID: true,
		},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:          "BTN",
			DisplayName:        "BTN",
			AuthKind:           "api_key_cookies_login_manual_2fa",
			SupportsCookieFile: true,
			SupportsLogin:      true,
			SupportsAutoLogin:  true,
			SupportsTOTP:       true,
			SupportsManual2FA:  true,
			RequiresAPIKey:     true,
			Notes:              []string{"API key is required for torrent resolution; cookies/login cover upload auth."},
		},
		AuthResolver: func(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
			return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, login, btnDefaultBaseURL)
		},
		AuthPolicy: &trackers.AuthPolicy{ //nolint:gosec // Operator messages name API keys; no credential is embedded.
			ResolveAPIKey:               func(cfg config.Config, _ config.TrackerConfig) string { return config.ResolveBTNAPIToken(cfg) },
			APIKeyRequiresUploadSession: true,
			MissingAPIKeyMessage:        "API key is required for torrent resolution; imported cookies or login credentials cover upload auth",
			UploadSessionMissingMessage: "API key covers torrent resolution; imported cookies or login credentials required for upload auth",
		},
	}
}

// Definition extends the shared standalone definition with BTN data and claim lookup.
type Definition struct {
	*standalone.Definition
	baseURL string
}

// New returns a fresh BTN definition from its tracker-local profile.
func New() *Definition {
	return &Definition{Definition: standalone.MustNew(Profile()), baseURL: btnDefaultBaseURL}
}
