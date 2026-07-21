// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cbr

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns CBR's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "CBR",
		BaseURL:      "https://capybarabr.com",
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:         buildName,
			ResolveCategoryID: categoryID,
		},
	}
}
func buildName(meta api.UploadSubject, cfg config.TrackerConfig) string {
	return unit3d.FormatLocalizedName(meta, cfg.TagForCustomRelease)
}
func categoryID(meta api.UploadSubject) string {
	if strings.EqualFold(unit3d.Category(meta), "TV") && meta.Anime {
		return "4"
	}
	return unit3d.DefaultCategoryID(meta)
}
