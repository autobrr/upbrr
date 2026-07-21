// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package blu

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns BLU's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "BLU",
		BaseURL:      "https://blutopia.cc",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
			ResolveCategoryID:   categoryID,
		},
	}
}

func categoryID(meta api.UploadSubject) string {
	if strings.Contains(strings.ToUpper(strings.TrimSpace(meta.Edition)), "FANRES") {
		return "3"
	}
	return unit3d.DefaultCategoryID(meta)
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "3",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
		"ENCODE": "12",
	}[unit3d.InferType(meta)]
}

func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"8640p": "10",
		"4320p": "11",
		"2160p": "1",
		"1440p": "2",
		"1080p": "2",
		"1080i": "3",
		"720p":  "5",
		"576p":  "6",
		"576i":  "7",
		"480p":  "8",
		"480i":  "9",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
