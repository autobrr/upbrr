// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package zdf

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ZDF's (0DayFiles) Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "ZDF",
		BaseURL: "https://0dayfiles.net",
		Site: unit3d.SiteProfile{
			CategoryIDs:       categoryIDs,
			ResolveCategoryID: categoryID,
		},
	}
}
