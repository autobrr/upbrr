// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tik

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns TIK's disc-type and category mappings, honoring explicit
// tracker-site overrides before metadata-derived classification.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "TIK",
		BaseURL: "https://cinematik.net",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:     typeID,
			ResolveCategoryID: categoryID,
		},
	}
}
