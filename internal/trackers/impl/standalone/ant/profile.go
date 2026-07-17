// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"maps"
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
)

// Profile returns ANT identity, preparation, dupe, rules, bans, and policies.
func Profile() standalone.Profile {
	groups := slices.Collect(maps.Keys(antBannedReleaseGroups))
	slices.Sort(groups)
	return standalone.Profile{
		Name:                 "ANT",
		BaseURL:              "https://anthelion.me",
		DescriptionGroup:     "ant",
		UploadContentMode:    trackers.UploadContentModeScreenshots,
		PrepareDescription:   prepareDescription,
		PrepareUpload:        prepareUpload,
		NewDuplicateAdapter:  newDuplicateAdapter,
		Rules:                &trackers.RuleSet{RequireMovieOnly: true},
		ArtifactPolicy:       &trackers.ArtifactPolicy{MaxPieceSizeMiB: 128, MaxTorrentBytes: 250 << 10},
		BannedGroups:         groups,
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "ANT"},
		DupePolicy:           &trackers.DupePolicy{DolbyVisionImpliesHDR: true},
		AudioPolicy: &trackers.AudioPolicy{
			AllowedLanguages: []string{"english"}, BlockEnglishOriginalWithForeign: true,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeMovie, AnyOf: []trackers.MetadataField{trackers.MetadataFieldTMDB},
			}},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker.anthelion.me"}, WorkingTrackerID: "1",
		},
	}
}

// Definition extends the shared standalone definition with ANT data lookup.
type Definition struct{ *standalone.Definition }

// New returns a fresh ANT definition from its tracker-local profile.
func New() *Definition { return &Definition{Definition: standalone.MustNew(Profile())} }
