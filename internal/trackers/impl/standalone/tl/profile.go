// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns TL identity, preparation, dupe, auth, and policy behavior,
// including the strict requirement for matching TMDB or IMDb metadata.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "TL",
		BaseURL:             baseURL,
		DescriptionGroup:    "tl",
		UploadContentMode:   trackers.UploadContentModeDescription,
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		ReleaseNamePolicy:   trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/tl/v1", resolveName, resolveSearchName),
		NewDuplicateAdapter: newDuplicateAdapter,
		ValidationPolicy:    validationPolicy(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: sourceFlag},
		AudioPolicy:          &trackers.AudioPolicy{AllowBloat: true},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker.tleechreload", "tracker.torrentleech"},
		},
		AuthCapability: &api.TrackerAuthCapability{
			TrackerID:          "TL",
			DisplayName:        "TL",
			AuthKind:           "passkey_or_cookies",
			SupportsCookieFile: true,
			RequiresPasskey:    true,
		},
		AuthPolicy: &trackers.AuthPolicy{
			ResolveRequirements: func(_ config.Config, cfg config.TrackerConfig) trackers.EffectiveAuthRequirements {
				if cfg.APIUpload {
					return authcontract.Requirements(
						"api_upload",
						false,
						[]trackers.AuthRequirement{trackers.AuthRequirementPasskey},
					)
				}
				return authcontract.Requirements(
					"form_upload",
					false,
					[]trackers.AuthRequirement{trackers.AuthRequirementStoredCookie},
				)
			},
		},
	}
}

// New returns a fresh TL definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
