// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ULCX's naming, duplicate, validation, and banned-group policy.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "ULCX",
		BaseURL:          "https://upload.cx",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v2",
		},
		DupePolicy: duplicatePolicy(),
	}
}
