// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns ANT identity, preparation, dupe, rules, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:               "ANT",
		BaseURL:            "https://anthelion.me",
		DescriptionGroup:   "ant",
		UploadContentMode:  trackers.UploadContentModeScreenshots,
		AuthCapability:     authcontract.APIKeyCapability("ANT"),
		PrepareDescription: prepareDescription,
		PrepareUpload:      prepareUpload,
		ReleaseNamePolicy: trackers.WithMovieYearProvider(
			trackers.SimpleSubjectReleaseNamePolicy("standalone/ant/v2", resolveUploadName),
			api.IdentityProviderTMDB,
		),
		NewDuplicateAdapter:  newDuplicateAdapter,
		Rules:                &trackers.RuleSet{RequireMovieOnly: true},
		ValidationPolicy:     validationPolicy(),
		ArtifactPolicy:       &trackers.ArtifactPolicy{MaxPieceSizeMiB: 128, MaxTorrentBytes: 250 << 10},
		BannedGroups:         bannedGroups(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "ANT", RequireAnnounce: true},
		DupePolicy: &trackers.DupePolicy{
			ID:         "ant/duplicate/v3",
			EvidenceID: "ant-dupes-trumping",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionCodec,
				trackers.DupeDimensionHDR,
			},
			HDRPartialMode:       trackers.DupeHDRPartialGenericMarker,
			HDRCompatibilityMode: trackers.DupeHDRCompatibilityDirectional,
			PrecedenceRules: []trackers.DupeRule{
				{
					ID:         "ant/duplicate/v3/exact_full_disc",
					Relation:   string(api.DupeRelationExactDuplicate),
					ReasonCode: "exact_identity",
					Conditions: []trackers.DupeCondition{
						{
							Dimension:       trackers.DupeDimensionMediaKind,
							TargetValues:    []string{"full_disc"},
							CandidateValues: []string{"full_disc"},
						},
						{
							Dimension:        trackers.DupeDimensionGroup,
							ValuesEqual:      true,
							RequiresComplete: true,
						},
						{
							Dimension:        trackers.DupeDimensionSize,
							ValuesEqual:      true,
							RequiresComplete: true,
						},
					},
				},
				{
					ID:         "ant/duplicate/v3/single_full_disc",
					Relation:   string(api.DupeRelationExistingPreferred),
					ReasonCode: "existing_full_disc",
					Conditions: []trackers.DupeCondition{{
						Dimension:       trackers.DupeDimensionMediaKind,
						TargetValues:    []string{"full_disc"},
						CandidateValues: []string{"full_disc"},
					}},
				},
			},
		},
		AudioPolicy: &trackers.AudioPolicy{
			AllowedLanguages: []string{"english"}, BlockEnglishOriginalWithForeign: true,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeMovie,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker.anthelion.me"},
			CommentURLPatterns: []string{"anthelion.me/torrents.php"},
			DetailIDPattern:    `(?i)[?&]torrentid=(\d+)`,
			WorkingTrackerID:   "1",
		},
	}
}

// Definition extends the shared standalone definition with ANT data lookup.
type Definition struct{ *standalone.Definition }

// New returns a fresh ANT definition from its tracker-local profile.
func New() *Definition { return &Definition{Definition: standalone.MustNew(Profile())} }
