// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rhd

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns RHD's German-oriented release-name construction, resolution
// mapping, tracker-owned rules, and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "RHD",
		BaseURL:      "https://rocket-hd.cc",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			BuildNameVersion:    "v3",
			ResolveResolutionID: resolutionID,
		},
	}
}
