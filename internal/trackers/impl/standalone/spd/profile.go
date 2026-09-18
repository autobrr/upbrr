// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns SPD identity, preparation, dupe, and policy behavior,
// including the strict requirement for matching TMDB or IMDb metadata.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "SPD",
		BaseURL:             baseURL,
		DescriptionGroup:    "spd",
		UploadContentMode:   trackers.UploadContentModeDescription,
		AuthCapability:      authcontract.APIKeyCapability("SPD"),
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ReleaseNamePolicy:   trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/spd/v1", resolveUploadName, resolveSearchName),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		BannedGroupPolicy: &trackers.BannedGroupPolicy{
			DefaultEndpoint:   baseURL + "/api/torrent/release-group/blacklist",
			EndpointPath:      "/api/torrent/release-group/blacklist",
			RequireAPIKey:     true,
			RawAPIKeyFallback: true,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		AudioPolicy: &trackers.AudioPolicy{
			AllowedLanguages: []string{"romanian"},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"ramjet.speedapp.io", "ramjet.speedapp.to", "ramjet.speedappio.org"},
		},
	}
}

// New returns a fresh SPD definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
