// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns TVC identity, preparation, dupe, and policy behavior.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "TVC",
		BaseURL:             "https://tvchaosuk.com",
		DescriptionGroup:    "tvc",
		UploadContentMode:   trackers.UploadContentModeDescription,
		PrepareDescription:  prepareDescription,
		PrepareUpload:       prepareUpload,
		NewDuplicateAdapter: newDuplicateAdapter,
		Rules:               rules(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny,
				AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB, trackers.MetadataFieldIMDB},
			}},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: "TVCHAOS"},
		ImageHostPolicy:       &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbb", "imgbox", "pixhost", "bam", "onlyimage"}},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"https://tvchaosuk.com"}},
	}
}

// New returns a fresh TVC definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
