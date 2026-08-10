// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns RMC's Unit3D site manifest. RMC only accepts movies
// released in 2000 or earlier and uses a nonstandard type/resolution ID
// table.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "RMC",
		BaseURL:          "https://retro-movies.club",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			BuildNameVersion:    "v1",
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}
