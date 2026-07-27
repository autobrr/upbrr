// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns HHD's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "HHD",
		BaseURL:      "https://homiehelpdesk.net",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v1",
		},
		DupePolicy: &trackers.DupePolicy{
			ID: "hhd/duplicate/v1",
			SearchScope: trackers.DupeSearchScope{
				IncludeEpisodes:    true,
				IncludeSeasonPacks: true,
				MaxPages:           100,
			},
			RequiredEvidence: trackers.DupeEvidenceRequirements{
				HDR:        true,
				Type:       true,
				Resolution: true,
				Codec:      true,
				Provider:   true,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionProvider,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionCodec,
				trackers.DupeDimensionHDR,
			},
			PrecedenceRules: hhdDupePrecedenceRules(),
		},
	}
}

func hhdDupePrecedenceRules() []trackers.DupeRule {
	rules := trackers.SeasonPackPrecedenceRules()
	return append(rules,
		trackers.DupeRule{
			ID:         "proposed_webdl_over_webrip",
			Relation:   "proposed_trumps",
			ReasonCode: "proposed_webdl_over_webrip",
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionType,
					TargetValues:    []string{"WEB-DL", "WEBDL"},
					CandidateValues: []string{"WEBRip", "WEB-RIP", "WEBRIP"},
				},
				{
					Dimension:        trackers.DupeDimensionResolution,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
				{
					Dimension:        trackers.DupeDimensionProvider,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
				{
					Dimension:        trackers.DupeDimensionCodec,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
				{
					Dimension:        trackers.DupeDimensionHDR,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
			},
		},
		trackers.DupeRule{
			ID:         "existing_webdl_over_webrip",
			Relation:   "existing_preferred",
			ReasonCode: "existing_webdl_over_webrip",
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionType,
					TargetValues:    []string{"WEBRip", "WEB-RIP", "WEBRIP"},
					CandidateValues: []string{"WEB-DL", "WEBDL"},
				},
				{
					Dimension:        trackers.DupeDimensionResolution,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
				{
					Dimension:        trackers.DupeDimensionProvider,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
				{
					Dimension:        trackers.DupeDimensionCodec,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
				{
					Dimension:        trackers.DupeDimensionHDR,
					ValuesEqual:      true,
					RequiresComplete: true,
				},
			},
		},
	)
}
