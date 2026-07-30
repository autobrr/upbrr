// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) int {
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	genres := strings.ToLower(resolveGenres(meta) + " " + resolveKeywords(meta))
	nonEnglish := !hasEnglishAudio(meta)
	switch category {
	case "TV":
		if strings.Contains(genres, "documentary") {
			if isSD(meta) {
				return 13
			}
			return 15
		}
		if meta.Anime {
			return 6
		}
		if strings.Contains(genres, "children") || strings.Contains(genres, "cartoon") {
			return 5
		}
		if meta.TVPack {
			if resolution == "2160p" {
				return 63
			}
			if isSD(meta) {
				return 6
			}
			return 4
		}
		if resolution == "2160p" {
			return 64
		}
		if resolution == "1080p" || resolution == "1080i" || resolution == "720p" {
			return 8
		}
		if isSD(meta) {
			if strings.Contains(strings.ToLower(meta.VideoEncode), "xvid") {
				return 9
			}
			return 48
		}
		return 47
	default:
		if strings.Contains(genres, "documentary") {
			if isSD(meta) {
				return 13
			}
			return 15
		}
		if meta.Anime {
			return 6
		}
		if resolution == "2160p" {
			if nonEnglish {
				return 60
			}
			return 62
		}
		if !isSD(meta) {
			if nonEnglish {
				return 18
			}
			return 16
		}
		if isSD(meta) {
			if nonEnglish {
				return 33
			}
			return 14
		}
		if nonEnglish {
			return 34
		}
		return 17
	}
}

func hasEnglishAudio(meta api.UploadSubject) bool {
	for _, language := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(language))
		if lower == "english" || lower == "en" {
			return true
		}
	}
	return false
}

func resolveGenres(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	}
	return strings.TrimSpace(meta.Release.Genre)
}

func isSD(meta api.UploadSubject) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "" || resolution == "480p" || resolution == "576p"
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}
