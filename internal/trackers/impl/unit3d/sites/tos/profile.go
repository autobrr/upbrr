// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tos

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns TOS's DVD/3D type mapping and French-subtitle-aware movie,
// episode, and TV-pack category mapping.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "TOS",
		BaseURL:      "https://theoldschool.cc",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		UploadArtifact: &trackers.UploadArtifactPolicy{
			Source: "TheOldSchool",
		},
		Site: unit3d.SiteProfile{
			ResolveTypeID:     typeID,
			ResolveCategoryID: categoryID,
		},
	}
}
