// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return cleanName(name)
	}
	if name := strings.TrimSpace(meta.ReleaseNameNoTag); name != "" {
		return cleanName(name)
	}
	if name := strings.TrimSpace(meta.Filename); name != "" {
		return cleanName(name)
	}
	return cleanName(pathutil.Base(meta.SourcePath))
}

func cleanName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " ", ".")
	value = strings.ReplaceAll(value, "..", ".")
	return strings.TrimSpace(value)
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.Identity.IMDBID != 0 || meta.Identity.TMDBID != 0 ||
		(meta.Identity.TVDBID != 0 && strings.EqualFold(string(meta.Identity.Category), string(api.CanonicalCategoryTV))) {
		return resolveUploadName(meta)
	}
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	query = strings.ReplaceAll(query, ": ", " ")
	query = strings.ReplaceAll(query, "’", "")
	query = strings.ReplaceAll(query, "'", "")
	return strings.Join(strings.Fields(query), " ")
}
