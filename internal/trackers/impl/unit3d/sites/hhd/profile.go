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
		Name:             "HHD",
		BaseURL:          "https://homiehelpdesk.net",
		Rules:            Rules(),
		ValidationPolicy: validationPolicy(),
		BannedGroups:     BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v2",
		},
		DupePolicy: &trackers.DupePolicy{
			ID:         "hhd/duplicate/v2",
			EvidenceID: "hhd-rules-naming",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionProvider,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionCodec,
				trackers.DupeDimensionHDR,
			},
			PrecedenceRules: hhdDupePrecedenceRules("hhd-rules-naming"),
		},
	}
}

func hhdDupePrecedenceRules(evidenceID string) []trackers.DupeRule {
	return []trackers.DupeRule{
		{
			ID:               "proposed_webdl_over_webrip",
			EvidenceID:       evidenceID,
			Relation:         "proposed_trumps",
			ReasonCode:       "proposed_webdl_over_webrip",
			OverridesGeneral: true,
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
		{
			ID:               "existing_webdl_over_webrip",
			EvidenceID:       evidenceID,
			Relation:         "existing_preferred",
			ReasonCode:       "existing_webdl_over_webrip",
			OverridesGeneral: true,
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
	}
}
