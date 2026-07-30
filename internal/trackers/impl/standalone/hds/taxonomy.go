// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) int {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return 15
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		return 40
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if strings.Contains(strings.ToLower(resolveGenres(meta)+" "+resolveKeywords(meta)), "documentary") {
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "2160p") {
			return 47
		}
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "1080p") || strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "1080i") {
			return 25
		}
		return 24
	}
	if meta.Anime {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 48
		case "1080p", "1080i":
			return 28
		default:
			return 27
		}
	}
	if category == "TV" {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 45
		case "1080p", "1080i":
			return 22
		default:
			return 21
		}
	}
	switch strings.TrimSpace(meta.Release.Resolution) {
	case "2160p":
		return 46
	case "1080p", "1080i":
		return 19
	default:
		return 18
	}
}

func resolveGenres(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres)
	case meta.ProviderMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres)
	default:
		return strings.TrimSpace(meta.Release.Genre)
	}
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}

func supportsHDSResolution(value string) bool {
	switch strings.TrimSpace(value) {
	case "2160p", "1080p", "1080i", "720p":
		return true
	default:
		return false
	}
}
