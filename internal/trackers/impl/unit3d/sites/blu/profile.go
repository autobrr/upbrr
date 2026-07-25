// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package blu

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns BLU's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "BLU",
		BaseURL:          "https://blutopia.cc",
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
			ResolveCategoryID:   categoryID,
		},
	}
}
