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
			BuildNameVersion: "v2",
		},
		DupePolicy: &trackers.DupePolicy{
			ID:         "ulcx/duplicate/v2",
			EvidenceID: "ulcx-upload-rules",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{trackers.DupeDimensionType, trackers.DupeDimensionResolution, trackers.DupeDimensionHDR},
			PrecedenceRules: append(
				trackers.SeasonPackPrecedenceRules("ulcx-upload-rules"),
				trackers.DirectionalMediaKindRules("ulcx-upload-rules", "web_dl", "web_rip")...,
			),
			SizeVariancePercent:     20,
			SizeVarianceResolutions: []string{"1080p"},
		},
	}
}
