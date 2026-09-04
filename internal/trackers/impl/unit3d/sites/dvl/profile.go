// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns DreadVault's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "DVL",
		BaseURL:          "https://dreadvault.org",
		DupePolicy:       duplicatePolicy(),
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny,
				AnyOf: []trackers.MetadataField{
					trackers.MetadataFieldTMDB,
					trackers.MetadataFieldIMDB,
					trackers.MetadataFieldTVDB,
				},
				Disposition: api.RuleDispositionStrict,
			}},
		},
	}
}
