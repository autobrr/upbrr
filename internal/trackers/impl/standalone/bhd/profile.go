// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns BHD identity, preparation, dupe, rules, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "BHD",
		BaseURL:            bhdBaseURL,
		DescriptionGroup:   "bhd",
		UploadContentMode:  trackers.UploadContentModeDescription,
		AuthCapability:     authcontract.APIKeyCapability("BHD"),
		PrepareDescription: prepareDescription,
		PrepareUpload:      prepareUpload,
		ReleaseNamePolicy: trackers.WithMovieYearProvider(
			trackers.SimpleSubjectReleaseNamePolicy("standalone/bhd/v2", resolveUploadName),
			api.IdentityProviderIMDB,
		),
		NewDuplicateAdapter:  newDuplicateAdapter,
		Rules:                rules(),
		ValidationPolicy:     validationPolicy(),
		BannedGroups:         bannedGroups(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "BHD"},
		AudioPolicy:          &trackers.AudioPolicy{BlockEnglishOriginalWithForeign: true},
		ImageHostPolicy:      &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbox", "imgbb", "pixhost", "bhd", "passtheimage"}},
		DupePolicy: &trackers.DupePolicy{
			ID:         "bhd/duplicate/v2",
			EvidenceID: "bhd-upload-rules",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionHDR,
			},
			SizeVariancePercent:     20,
			SizeVarianceResolutions: []string{"1080p"},
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{
				{
					Scope:       trackers.MetadataScopeAny,
					AnyOf:       []trackers.MetadataField{trackers.MetadataFieldIMDBIDOnly},
					Disposition: api.RuleDispositionStrict,
				},
				{
					Scope:       trackers.MetadataScopeAny,
					AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly},
					Disposition: api.RuleDispositionStrict,
				},
			},
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
