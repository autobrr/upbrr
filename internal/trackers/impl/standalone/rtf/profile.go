// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns RTF identity, preparation, dupe, auth, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                 "RTF",
		BaseURL:              defaultBaseURL,
		DescriptionGroup:     "rtf",
		UploadContentMode:    trackers.UploadContentModeScreenshots,
		PrepareDescription:   prepareDescription,
		PrepareUpload:        prepareUpload,
		ReleaseNamePolicy:    trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/rtf/v1", resolveUploadName, resolveSearchName),
		NewDuplicateAdapter:  newDuplicateAdapter,
		DupePolicy:           duplicatePolicy(),
		ValidationPolicy:     validationPolicy(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: sourceFlag},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"peer.retroflix"},
			CommentURLPatterns: []string{defaultBaseURL},
			DetailIDPattern:    "(?i)retroflix\\.club/browse/t/(\\d+)",
		},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:         "RTF",
			DisplayName:       "RTF",
			AuthKind:          "api_key_credential_refresh",
			SupportsLogin:     true,
			SupportsAutoLogin: true,
			RequiresAPIKey:    true,
		},
		AuthPolicy: &trackers.AuthPolicy{
			ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
				"api_key_or_refresh",
				false,
				[]trackers.AuthRequirement{trackers.AuthRequirementAPIKey},
				[]trackers.AuthRequirement{trackers.AuthRequirementCredentialLogin},
			)),
		},
		AuthResolver: func(ctx context.Context, cfg config.TrackerConfig, dbPath string, request api.TrackerAuthLoginRequest) error {
			return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, request, defaultBaseURL)
		},
	}
}

// New returns a fresh RTF definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
