// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategory(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err == nil && category != "" {
		return strings.ToUpper(string(category))
	}
	return strings.ToUpper(strings.TrimSpace(string(meta.Identity.Category)))
}

func resolveMediaType(meta api.UploadSubject) string {
	if resolveCategory(meta) == "TV" {
		if meta.TVPack {
			return "show_season"
		}
		return "show_episode"
	}
	return "movie"
}

func isDiscType(value string) bool {
	discType := strings.ToLower(strings.TrimSpace(value))
	return discType == "bdmv" || discType == "dvd" || discType == "hddvd" || discType == "hd-dvd"
}

func resolveEdition(meta api.UploadSubject) string {
	if len(meta.Release.Edition) > 0 {
		return strings.Join(meta.Release.Edition, " ")
	}
	return ""
}
