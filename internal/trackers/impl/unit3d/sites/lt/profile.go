// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lt

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns LT's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "LT",
		BaseURL:      "https://lat-team.com",
		BannedGroups: BannedGroups(),
	}
}
