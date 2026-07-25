// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var categoryMap = map[string]string{
	"comedy":          "29",
	"current affairs": "45",
	"documentary":     "5",
	"drama":           "11",
	"entertainment":   "14",
	"factual":         "19",
	"foreign":         "43",
	"kids":            "32",
	"movies":          "44",
	"news":            "54",
	"reality":         "52",
	"soaps":           "30",
	"sci-fi":          "33",
	"sport":           "42",
	"holding bin":     "53",
}

func resolveCategory(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB.OriginalLanguage != "" && !strings.EqualFold(meta.ProviderMetadata.TMDB.OriginalLanguage, "en") &&
		!strings.EqualFold(meta.ProviderMetadata.TMDB.OriginalLanguage, "ga") &&
		!strings.EqualFold(meta.ProviderMetadata.TMDB.OriginalLanguage, "gd") &&
		!strings.EqualFold(meta.ProviderMetadata.TMDB.OriginalLanguage, "cy") {
		return categoryMap["foreign"]
	}
	genres := strings.ToLower(genresText(meta))
	for key, value := range categoryMap {
		if key != "foreign" && strings.Contains(genres, key) {
			return value
		}
	}
	if !isTV(meta) {
		return categoryMap["movies"]
	}
	return categoryMap["holding bin"]
}

func resolveResolution(meta api.UploadSubject) string {
	if meta.TVPack {
		switch meta.Release.Resolution {
		case "1080p", "1080i":
			return "HD1080p Pack"
		case "720p":
			return "HD720p Pack"
		default:
			return "SD Pack"
		}
	}
	switch meta.Release.Resolution {
	case "1080p", "1080i":
		return "HD1080p"
	case "720p":
		return "HD720p"
	default:
		return "SD"
	}
}

func boolNum(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func isTV(meta api.UploadSubject) bool {
	return isTVCategory(meta)
}

// isTVCategory reports whether TVC payloads may include TVDB-specific fields.
// Only canonical TV identity enables TVDB-specific fields.
func isTVCategory(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func isMovieCategory(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryMovie
}

func categoryName(meta api.UploadSubject) string {
	if isTV(meta) {
		return "TV"
	}
	if isMovieCategory(meta) {
		return "MOVIE"
	}
	return ""
}
