// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns NBL identity, preparation, dupe, rules, bans, and policies,
// including the strict TVmaze metadata requirement for TV uploads.
func Profile() standalone.Profile {
	return standalone.Profile{
		Name:                "NBL",
		BaseURL:             "https://nebulance.io",
		DescriptionGroup:    "nbl",
		UploadContentMode:   trackers.UploadContentModeNone,
		AuthCapability:      authcontract.APIKeyCapability("NBL"),
		PrepareUpload:       prepareUpload,
		ReleaseNamePolicy:   trackers.SimpleSubjectReleaseNameSearchPolicy("standalone/nbl/v2", resolveUploadName, resolveSearchName),
		NewDuplicateAdapter: newDuplicateAdapter,
		Rules:               rules(),
		ValidationPolicy:    validationPolicy(),
		BannedGroups:        bannedGroups(),
		DupePolicy: &trackers.DupePolicy{
			ID:         "nbl/duplicate/v2",
			EvidenceID: "nbl-uploading-overview",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{trackers.DupeDimensionHDR},
			HDRSlotMode:    trackers.DupeHDRSlotModeGeneric,
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeTV,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTVmaze},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		UploadArtifactPolicy:  &trackers.UploadArtifactPolicy{Source: "NBL"},
		TorrentIdentityPolicy: &trackers.TorrentIdentityPolicy{TrackerURLPatterns: []string{"tracker.nebulance"}},
	}
}

// New returns a fresh NBL definition from its tracker-local profile.
func New() *standalone.Definition { return standalone.MustNew(Profile()) }
