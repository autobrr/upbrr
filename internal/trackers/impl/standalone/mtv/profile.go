// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns MTV identity, preparation, dupe, auth, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "MTV",
		BaseURL:            mtvBaseURL,
		DescriptionGroup:   "mtv",
		PrepareDescription: prepareDescription,
		PrepareUpload: func(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
			return prepareUploadAt(ctx, req, mtvBaseURL)
		},
		NewDuplicateAdapter: newDuplicateAdapter,
		BannedGroups:        bannedGroups(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{
				{Scope: trackers.MetadataScopeAny, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB}},
				{Scope: trackers.MetadataScopeTV, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVDBTitle}},
			},
		},
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "MTV"},
		AudioPolicy:          &trackers.AudioPolicy{BlockEnglishOriginalWithForeign: true},
		ImageHostPolicy:      &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbox", "imgbb"}},
		DupePolicy:           &trackers.DupePolicy{ContainsFilenameMatch: true, NormalizeMTVName: true},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker.morethantv"}, SearchPreference: trackers.TorrentSearchPreferenceSmallPieces,
		},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:          "MTV",
			DisplayName:        "MTV",
			AuthKind:           "api_key_cookies_login_manual_2fa",
			SupportsCookieFile: true,
			SupportsLogin:      true,
			SupportsAutoLogin:  true,
			SupportsTOTP:       true,
			SupportsManual2FA:  true,
			RequiresAPIKey:     true,
			Notes:              []string{"API key covers Torznab/search; cookies/login cover upload authkey."},
		},
		AuthResolver: func(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
			return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, login, mtvBaseURL)
		},
		AuthPolicy: &trackers.AuthPolicy{
			APIKeyRequiresUploadSession: true,
			CookieCompletesAPIKeyAuth:   true,
			UploadSessionMissingMessage: "API key covers Torznab/search; imported cookies or login credentials required for upload auth",
		},
	}
}

// New returns a fresh MTV definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
