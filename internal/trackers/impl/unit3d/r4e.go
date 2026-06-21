// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func siteR4EProfile() unit3DSiteProfile {
	return unit3DSiteProfile{resolveCategoryID: resolveUnit3DR4ECategoryID}
}

func resolveUnit3DR4ECategoryID(meta api.PreparedMetadata) string {
	genreIDs := ""
	if meta.ExternalMetadata.TMDB != nil {
		genreIDs = strings.TrimSpace(meta.ExternalMetadata.TMDB.GenreIDs)
	}
	isDoc := false
	if genreIDs != "" {
		for value := range strings.SplitSeq(genreIDs, ",") {
			if strings.TrimSpace(value) == "99" {
				isDoc = true
				break
			}
		}
	}
	category := resolveUnit3DCategory(meta)
	if strings.EqualFold(category, "MOVIE") {
		if isDoc {
			return "66"
		}
		return "70"
	}
	if strings.EqualFold(category, "TV") {
		if isDoc {
			return "2"
		}
		return "79"
	}
	return "24"
}
