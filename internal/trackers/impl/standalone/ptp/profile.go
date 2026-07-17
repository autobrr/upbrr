// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns PTP identity, preparation, dupe, auth, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "PTP",
		BaseURL:            ptpBaseURL,
		DescriptionGroup:   "ptp",
		UploadContentMode:  trackers.UploadContentModeDescription,
		PrepareDescription: prepareDescription,
		PrepareUpload: func(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
			return prepareUploadAt(ctx, req, ptpBaseURL)
		},
		NewDuplicateAdapter: func(deps dupe.Dependencies) dupe.Adapter { return newDuplicateAdapterAt(deps, ptpBaseURL) },
		Rules:               &trackers.RuleSet{RequireMovieUnlessTVPack: true},
		BannedGroups:        bannedGroups(),
		DataPolicy:          &trackers.DataLookupPolicy{Cooldown: time.Minute},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{Requirements: []trackers.MetadataRequirement{{
			Scope:       trackers.MetadataScopeAny,
			AnyOf:       []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly},
			Disposition: api.RuleDispositionAdvisory,
		}}},
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "PTP"},
		ImageHostPolicy: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"pixhost", "imgbb", "onlyimage", "ptscreens", "passtheimage"},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{ptpBaseURL},
			CommentURLPatterns: []string{ptpBaseURL},
			DetailIDPattern:    "torrentid=(\\d+)",
		},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:          "PTP",
			DisplayName:        "PTP",
			AuthKind:           "cookies_login_manual_2fa",
			SupportsCookieFile: true,
			SupportsLogin:      true,
			SupportsAutoLogin:  true,
			SupportsTOTP:       true,
			SupportsManual2FA:  true,
		},
		AuthResolver: func(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
			return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, login, ptpBaseURL)
		},
		AuthPolicy: &trackers.AuthPolicy{LoginRequiresAnnounceURL: true},
	}
}

// Definition extends the shared standalone definition with PTP data lookup and testable endpoints.
type Definition struct {
	*standalone.Definition
	baseURL string
}

// New returns a fresh PTP definition from its tracker-local profile.
func New() *Definition {
	return &Definition{Definition: standalone.MustNew(Profile()), baseURL: ptpBaseURL}
}
