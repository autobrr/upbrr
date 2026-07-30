// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategory(meta api.UploadSubject) string {
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return ""
	}
	originalLanguage := ""
	if meta.ProviderMetadata.TMDB != nil {
		originalLanguage = strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	}
	if meta.Anime {
		return "34"
	}
	if !isTV(meta) {
		if originalLanguage != "" && !strings.EqualFold(originalLanguage, "en") {
			return "36"
		}
		if containsWord(genresText(meta), "Documentary") {
			return "29"
		}
		if meta.Release.Resolution == "2160p" {
			return "47"
		}
		if strings.EqualFold(meta.DiscType, "BDMV") || strings.EqualFold(meta.Type, "REMUX") && strings.EqualFold(meta.Source, "BluRay") {
			return "13"
		}
		if strings.EqualFold(meta.Type, "ENCODE") && strings.EqualFold(meta.Source, "BluRay") {
			return "14"
		}
		if strings.EqualFold(meta.DiscType, "DVD") || strings.Contains(strings.ToUpper(meta.Source), "DVD") && strings.EqualFold(meta.Type, "REMUX") {
			return "12"
		}
		if strings.Contains(strings.ToUpper(meta.Source), "DVD") || strings.EqualFold(meta.Type, "DVDRIP") {
			return "11"
		}
		if strings.Contains(strings.ToUpper(meta.Type), "WEB") {
			return "37"
		}
		if strings.EqualFold(meta.Type, "HDTV") {
			return "43"
		}
	}
	if isTV(meta) && originalLanguage != "" && !strings.EqualFold(originalLanguage, "en") {
		return "44"
	}
	if meta.TVPack {
		return "27"
	}
	if isSD(meta.Release.Resolution) {
		return "26"
	}
	return "32"
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func boolWord(cond bool, yes string, no string) string {
	if cond {
		return yes
	}
	return no
}

func isTV(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}
