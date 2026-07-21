// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ihd

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns IHD's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "IHD",
		BaseURL: "https://infinityhd.net",
		Site: unit3d.SiteProfile{
			ResolveResolutionID: resolutionID,
			ResolveCategoryID:   categoryID,
		},
	}
}

func categoryID(meta api.UploadSubject) string {
	category := unit3d.Category(meta)
	if strings.EqualFold(category, "TV") && meta.Anime {
		return "3"
	}
	if strings.EqualFold(category, "MOVIE") && meta.Anime {
		return "4"
	}
	return unit3d.DefaultCategoryID(meta)
}

func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1440p": "3",
		"1080p": "3",
		"1080i": "4",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
