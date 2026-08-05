// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mns

import "github.com/autobrr/upbrr/internal/trackers/impl/unit3d"

// Profile returns MNS's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "MNS",
		BaseURL: "https://midnightscene.cc",
		Rules:   Rules(),
	}
}
