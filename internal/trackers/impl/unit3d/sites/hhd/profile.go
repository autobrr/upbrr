// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns HHD's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "HHD",
		BaseURL:      "https://homiehelpdesk.net",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
	}
}
