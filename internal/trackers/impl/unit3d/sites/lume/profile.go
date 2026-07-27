// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns LUME's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "LUME",
		BaseURL:          "https://luminarr.me",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v1",
		},
		DupePolicy: &trackers.DupePolicy{
			ID: "lume/duplicate/v1",
			SearchScope: trackers.DupeSearchScope{
				IncludeEpisodes:    true,
				IncludeSeasonPacks: true,
				MaxPages:           100,
			},
			RequiredEvidence: trackers.DupeEvidenceRequirements{
				HDR:        true,
				Type:       true,
				Resolution: true,
				Provider:   true,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionEdition,
				trackers.DupeDimensionProvider,
				trackers.DupeDimensionHDR,
			},
			PrecedenceRules:     trackers.SeasonPackPrecedenceRules(),
			SizeVariancePercent: 20,
			SizeVarianceTypes:   []string{"ENCODE"},
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			TRaSHGuideURL: "https://raw.githubusercontent.com/TRaSH-Guides/Guides/refs/heads/master/docs/json/radarr/cf/lq.json",
		},
	}
}
