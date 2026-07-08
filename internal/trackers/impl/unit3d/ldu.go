// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func siteLDUProfile() unit3DSiteProfile {
	return unit3DSiteProfile{resolveCategoryID: resolveUnit3DLDUCategoryID}
}

func resolveUnit3DLDUCategoryID(meta api.PreparedMetadata) string {
	category := resolveUnit3DCategory(meta)
	genres := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(meta.Release.Genre),
		resolveKeywords(meta),
		resolveTMDBGenres(meta),
		resolveIMDBGenres(meta),
	}, ",")))
	hasEnglishAudio := hasEnglishLanguage(meta.AudioLanguages)
	hasEnglishSubs := hasEnglishLanguage(meta.SubtitleLanguages)
	containsDubbed := strings.Contains(strings.ToLower(strings.TrimSpace(meta.Audio)), "dubbed")
	edition := strings.ToLower(strings.TrimSpace(meta.Edition))

	if strings.EqualFold(category, "MOVIE") {
		if meta.Anime || meta.ExternalIDs.MALID != 0 {
			return "8"
		}
		if strings.Contains(edition, "fanedit") || strings.Contains(edition, "fanres") {
			return "12"
		}
		if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
			return "21"
		}
		if hasAdultToken(genres) {
			if !hasEnglishAudio && !hasEnglishSubs {
				return "45"
			}
			return "6"
		}
		if strings.Contains(genres, "documentary") {
			return "17"
		}
		if strings.Contains(genres, "musical") {
			return "25"
		}
		if !hasEnglishAudio && !hasEnglishSubs {
			return "22"
		}
		if containsDubbed {
			return "27"
		}
		return "1"
	}

	if strings.EqualFold(category, "TV") {
		if meta.Anime || meta.ExternalIDs.MALID != 0 {
			return "9"
		}
		if strings.Contains(genres, "documentary") {
			return "40"
		}
		if !hasEnglishAudio && !hasEnglishSubs {
			return "29"
		}
		if meta.TVPack {
			return "2"
		}
		if containsDubbed {
			return "31"
		}
		return "41"
	}

	return resolveUnit3DCategoryID(meta)
}
