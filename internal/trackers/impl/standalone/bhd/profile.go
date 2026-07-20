// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns BHD identity, preparation, dupe, rules, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                 "BHD",
		BaseURL:              bhdBaseURL,
		DescriptionGroup:     "bhd",
		UploadContentMode:    trackers.UploadContentModeDescription,
		PrepareDescription:   prepareDescription,
		PrepareUpload:        prepareUpload,
		NewDuplicateAdapter:  newDuplicateAdapter,
		Rules:                rules(),
		BannedGroups:         bannedGroups(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "BHD"},
		AudioPolicy:          &trackers.AudioPolicy{BlockEnglishOriginalWithForeign: true},
		ImageHostPolicy:      &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbox", "imgbb", "pixhost", "bhd", "bam"}},
		DupePolicy: &trackers.DupePolicy{
			MatchAggregateSize:    true,
			NormalizeDDPlusName:   true,
			SDMatchesHD:           true,
			CompareDVDResolution:  true,
			AllowSizeVariance1080: true,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeMovie,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldIMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://beyond-hd.me", "tracker.beyond-hd.me"},
			CommentURLPatterns: []string{"https://beyond-hd.me"},
			DetailIDPattern:    "details/(\\d+)",
		},
	}
}

// Definition extends the shared standalone definition with BHD data lookup.
type Definition struct{ *standalone.Definition }

// New returns a fresh BHD definition from its tracker-local profile.
func New() *Definition { return &Definition{Definition: standalone.MustNew(Profile())} }
