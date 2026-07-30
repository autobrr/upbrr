// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategory(meta api.UploadSubject) string {
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return ""
	}
	romanian := hasRomanian(meta)
	if containsWord(genresText(meta), "documentary") || containsWord(keywordsText(meta), "documentary") {
		if romanian {
			return "63"
		}
		return "9"
	}
	if meta.Anime {
		return "3"
	}
	if isTV(meta) {
		if meta.TVPack {
			if romanian {
				return "66"
			}
			return "41"
		}
		if isSD(meta.Release.Resolution) {
			if romanian {
				return "46"
			}
			return "45"
		}
		if romanian {
			return "44"
		}
		return "43"
	}
	if meta.Release.Resolution == "2160p" && !strings.EqualFold(meta.Type, "DISC") {
		if romanian {
			return "57"
		}
		return "61"
	}
	if strings.EqualFold(meta.Type, "DISC") {
		if romanian {
			return "24"
		}
		return "17"
	}
	if romanian {
		return "29"
	}
	return "8"
}

func hasRomanian(meta api.UploadSubject) bool {
	for _, value := range append([]string{}, append(meta.AudioLanguages, meta.SubtitleLanguages...)...) {
		if strings.EqualFold(strings.TrimSpace(value), "romanian") {
			return true
		}
	}
	if meta.ProviderMetadata.TMDB != nil {
		for _, code := range meta.ProviderMetadata.TMDB.OriginCountry {
			if strings.EqualFold(strings.TrimSpace(code), "RO") {
				return true
			}
		}
	}
	return false
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func isSD(res string) bool {
	return strings.HasPrefix(strings.TrimSpace(res), "480") || strings.HasPrefix(strings.TrimSpace(res), "576") ||
		strings.HasPrefix(strings.TrimSpace(res), "540")
}

func isTV(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}
