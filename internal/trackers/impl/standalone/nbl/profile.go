// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns NBL identity, preparation, dupe, rules, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "NBL",
		BaseURL:             "https://nebulance.io",
		DescriptionGroup:    "nbl",
		UploadContentMode:   trackers.UploadContentModeNone,
		PrepareUpload:       prepareUpload,
		NewDuplicateAdapter: newDuplicateAdapter,
		Rules:               rules(),
		BannedGroups:        bannedGroups(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeTV,
				AnyOf: []trackers.MetadataField{trackers.MetadataFieldTVmaze},
			}},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: "NBL"},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"tracker.nebulance"}},
	}
}

// New returns a fresh NBL definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
