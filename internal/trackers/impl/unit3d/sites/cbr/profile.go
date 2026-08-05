// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cbr

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns CBR's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "CBR",
		BaseURL:      "https://capybarabr.com",
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:         buildName,
			BuildNameVersion:  "v1",
			ResolveCategoryID: categoryID,
		},
	}
}
