// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tos

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns TOS's DVD/3D type mapping and French-subtitle-aware movie,
// episode, and TV-pack category mapping.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "TOS",
		BaseURL:      "https://theoldschool.cc",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		UploadArtifact: &trackers.UploadArtifactPolicy{
			Source: "TheOldSchool",
		},
		Site: unit3d.SiteProfile{
			ResolveTypeID:     typeID,
			ResolveCategoryID: categoryID,
		},
	}
}

func categoryID(meta api.UploadSubject) string {
	tag := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")))
	subFrench := strings.Contains(tag, "vostfr") || strings.Contains(tag, "subfrench")
	if strings.EqualFold(unit3d.Category(meta), "TV") {
		if meta.TVPack {
			if subFrench {
				return "9"
			}
			return "8"
		}
		if subFrench {
			return "7"
		}
		return "2"
	}
	if subFrench {
		return "6"
	}
	return "1"
}

func typeID(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "7"
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		return "8"
	}
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"ENCODE": "3",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
	}[unit3d.InferType(meta)]
}
