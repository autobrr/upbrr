// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func hasMandarin(meta api.UploadSubject) bool {
	for _, values := range [][]string{meta.AudioLanguages, meta.SubtitleLanguages} {
		for _, value := range values {
			lower := strings.ToLower(strings.TrimSpace(value))
			if strings.Contains(lower, "mandarin") || strings.Contains(lower, "chinese") {
				return true
			}
		}
	}
	return false
}

func resolveType(meta api.UploadSubject) string {
	if _, err := meta.Identity.RequireCategory(); err != nil {
		return ""
	}
	if meta.Anime {
		return "407"
	}
	if isTV(meta) {
		return "405"
	}
	return "404"
}

func isTV(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}
