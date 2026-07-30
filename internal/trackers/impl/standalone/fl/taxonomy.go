// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) int {
	if meta.Anime {
		return 24
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	switch {
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD"):
		if hasRomanianSub(meta) {
			return 3
		}
		return 2
	case category == "TV":
		if resolution == "2160p" {
			return 27
		}
		if isSD(meta) {
			return 23
		}
		return 21
	default:
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if resolution == "2160p" {
				return 26
			}
			return 20
		}
		if resolution == "2160p" {
			return 6
		}
		if isSD(meta) {
			return 1
		}
		if hasRomanianSub(meta) {
			return 19
		}
		return 4
	}
}

func resolveGenres(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	}
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	}
	return strings.TrimSpace(meta.Release.Genre)
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}

func isSD(meta api.UploadSubject) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "480p" || resolution == "576p"
}

func hasRomanianAudio(meta api.UploadSubject) bool {
	for _, lang := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "romanian" || lower == "ro" {
			return true
		}
	}
	return false
}

func hasRomanianSub(meta api.UploadSubject) bool {
	for _, lang := range meta.SubtitleLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "romanian" || lower == "ro" {
			return true
		}
	}
	return false
}
