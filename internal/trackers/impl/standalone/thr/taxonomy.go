// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var (
	subtitleMap = map[string]string{
		"croatian":  "1",
		"english":   "2",
		"bosnian":   "3",
		"serbian":   "4",
		"slovenian": "5",
	}
	idPattern = regexp.MustCompile(`id=(\d+)`)
)

func resolveCategory(meta api.UploadSubject) string {
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return ""
	}
	if containsWord(genresText(meta), "documentary") || containsWord(keywordsText(meta), "documentary") {
		return "12"
	}
	switch categoryName(meta) {
	case "MOVIE":
		if strings.EqualFold(meta.DiscType, "BDMV") {
			return "40"
		}
		if strings.EqualFold(meta.DiscType, "DVD") || strings.EqualFold(meta.DiscType, "HDDVD") {
			return "14"
		}
		if isSD(meta.Release.Resolution) {
			return "4"
		}
		return "17"
	case "TV":
		if isSD(meta.Release.Resolution) {
			return "7"
		}
		return "34"
	default:
		if meta.Anime {
			return "31"
		}
	}
	return ""
}

func resolveSubtitles(meta api.UploadSubject) []string {
	result := make([]string, 0, len(meta.SubtitleLanguages))
	seen := map[string]struct{}{}
	for _, lang := range meta.SubtitleLanguages {
		id := subtitleMap[strings.ToLower(strings.TrimSpace(lang))]
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func categoryName(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	if category == api.CanonicalCategoryTV {
		return "TV"
	}
	return "MOVIE"
}
