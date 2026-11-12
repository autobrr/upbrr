// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	antHost    = "anthelion.dev"
	antBaseURL = "https://" + antHost
	antAPIURL  = antBaseURL + "/api.php"
)

// Profile returns ANT identity, preparation, dupe, rules, bans, and policies.
func Profile() standalone.Profile {
	return standalone.Profile{
		ConnectionProbe:   trackers.ConnectionProbe{Test: testConnection, DraftConfig: trackers.APIKeyProbeConfig},
		UsesMenuImages:    true,
		Name:              "ANT",
		BaseURL:           antBaseURL,
		DescriptionGroup:  "ant",
		UploadContentMode: trackers.UploadContentModeOptionalDescription,
		CategoryPolicies: map[api.CanonicalCategory]trackers.CategoryPolicy{
			api.CanonicalCategoryTV: {
				UploadContentMode: trackers.UploadContentModeNone,
				DupePolicy: &trackers.DupePolicy{
					ID:             "ant/tv-duplicate/v2",
					TargetTVScope:  resolveTargetTVScope,
					EvidenceID:     "ant-combined-api-nbl-tv-rules",
					SearchScope:    trackers.DupeSearchScope{MaxPages: 100},
					SlotDimensions: []trackers.DupeDimension{trackers.DupeDimensionHDR},
					HDRSlotMode:    trackers.DupeHDRSlotModeGeneric,
				},
			},
		},
		AuthCapability:          authcontract.APIKeyCapability("ANT"),
		PrepareDescription:      prepareDescription,
		PrepareUpload:           prepareUpload,
		ProjectionQuestionnaire: projectionQuestionnaire,
		ReleaseNamePolicy: trackers.WithMovieYearProvider(
			trackers.SimpleSubjectReleaseNamePolicy("standalone/ant/v3", resolveUploadName),
			api.IdentityProviderTMDB,
		),
		NewDuplicateAdapter:  newDuplicateAdapter,
		Rules:                &trackers.RuleSet{RequireUniqueID: true},
		ValidationPolicy:     validationPolicy(),
		ArtifactPolicy:       &trackers.ArtifactPolicy{MaxPieceSizeMiB: 128, MaxTorrentBytes: 250 << 10},
		BannedGroups:         bannedGroups(),
		UploadArtifactPolicy: &trackers.UploadArtifactPolicy{Source: "ANT", RequireAnnounce: true},
		DupePolicy:           duplicatePolicy(),
		DataPolicy:           &trackers.DataLookupPolicy{Cooldown: 15 * time.Second, LegacyExternalIDsNeedRefresh: true},
		AudioPolicy: &trackers.AudioPolicy{
			AllowedLanguages: []string{"english"}, BlockEnglishOriginalWithForeign: true,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeMovie,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
				Disposition: api.RuleDispositionStrict,
			}, {
				Scope:       trackers.MetadataScopeTV,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTVDB, trackers.MetadataFieldTMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"tracker." + antHost, "tracker.anthelion.me"},
			CommentURLPatterns: []string{antHost + "/torrents.php", "anthelion.me/torrents.php"},
			DetailIDPattern:    `(?i)[?&]torrentid=(\d+)`,
			WorkingTrackerID:   "1",
		},
	}
}

// Definition extends the shared standalone definition with ANT data lookup.
type Definition struct{ *standalone.Definition }

// New returns a fresh ANT definition from its tracker-local profile.
func New() *Definition { return &Definition{Definition: standalone.MustNew(Profile())} }
