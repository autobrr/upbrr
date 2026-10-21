// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sc

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns SC's (SeedCore) Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "SC",
		BaseURL:      "https://seedcore.net",
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
