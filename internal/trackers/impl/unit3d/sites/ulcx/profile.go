// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ULCX's WEB-DL hybrid-name normalization, size-variance dupe
// policy, tracker-owned rules, and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "ULCX",
		BaseURL:          "https://upload.cx",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v1",
		},
		DupePolicy: &trackers.DupePolicy{
			ID: "ulcx/duplicate/v1",
			SearchScope: trackers.DupeSearchScope{
				IncludeEpisodes:    true,
				IncludeSeasonPacks: true,
				MaxPages:           100,
			},
			RequiredEvidence:        trackers.DupeEvidenceRequirements{HDR: true},
			SlotDimensions:          []trackers.DupeDimension{trackers.DupeDimensionType, trackers.DupeDimensionResolution, trackers.DupeDimensionHDR},
			SizeVariancePercent:     20,
			SizeVarianceResolutions: []string{"1080p"},
		},
	}
}
