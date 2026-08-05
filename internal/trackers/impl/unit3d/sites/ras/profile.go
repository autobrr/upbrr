// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ras

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns RAS's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "RAS",
		BaseURL:      "https://rastastugan.org",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
	}
}
