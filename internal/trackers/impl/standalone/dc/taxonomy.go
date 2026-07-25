// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) int {
	resolution := strings.ToLower(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Resolution, meta.ReleaseNameNoTag, meta.ReleaseName)))
	category := categoryOf(meta)
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if category == "TV" {
			return 14
		}
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "2160p") {
			return 38
		}
		return 3
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		if category == "TV" {
			return 11
		}
		return 1
	}
	if category == "TV" && meta.TVPack {
		return 12
	}
	if isSD(meta) {
		if category == "TV" {
			return 10
		}
		return 2
	}
	switch category {
	case "TV":
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 13
		case "1080p", "1080i":
			return 9
		default:
			return 8
		}
	default:
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 4
		case "1080p", "1080i":
			return 6
		default:
			_ = resolution
			return 5
		}
	}
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func isSD(meta api.UploadSubject) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "480p" || resolution == "576p" || resolution == ""
}
