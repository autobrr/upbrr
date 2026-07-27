// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns AITHER's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:        "AITHER",
		BaseURL:     "https://aither.cc",
		Rules:       Rules(),
		AudioPolicy: AudioPolicy(),
		Site: unit3d.SiteProfile{
			BuildName:              buildName,
			BuildNameVersion:       "v1",
			ApplyAdditionalPayload: additionalPayload,
		},
		DupePolicy: &trackers.DupePolicy{
			ID: "aither/duplicate/v1",
			SearchScope: trackers.DupeSearchScope{
				IncludeEpisodes:    true,
				IncludeSeasonPacks: true,
				MaxPages:           100,
			},
			RequiredEvidence: trackers.DupeEvidenceRequirements{HDR: true},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionHDR,
			},
			SizeVariancePercent: 20,
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			EndpointPath:  "/api/blacklists/releasegroups",
			RequireAPIKey: true,
		},
		ClaimPolicy: &trackers.ClaimPolicy{
			APIBacked: true,
		},
	}
}
