// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"github.com/autobrr/upbrr/internal/trackers"
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
		NewDuplicateAdapter: newDuplicateAdapter,
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
		AuthCapability: standalone.CookieAuthCapability("TL"),
	}
}

// New returns a fresh TL definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
