// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns RMC's Unit3D site manifest. RMC requires current TMDB movie
// metadata from 2000 or earlier and uses a nonstandard type/resolution ID table.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "RMC",
		BaseURL:          "https://retro-movies.club",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDBTitle},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			BuildNameVersion:    "v1",
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}
