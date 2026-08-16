// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns SAM's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "SAM",
		BaseURL: "https://samaritano.cc",
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "samaritano",
			OwnedHosts:             []string{"samaritano"},
			EnableWithImageHosting: true,
		},
		Site: unit3d.SiteProfile{
			BuildName:         buildName,
			BuildNameVersion:  "v1",
			ResolveCategoryID: categoryID,
		},
	}
}
