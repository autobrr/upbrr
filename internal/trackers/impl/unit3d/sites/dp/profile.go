// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns DP's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "DP",
		BaseURL:      "https://darkpeers.org",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v1",
		},
	}
}
