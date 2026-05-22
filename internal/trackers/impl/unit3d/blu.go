// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func siteBLUProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveTypeID:       resolveUnit3DBLUTypeID,
		resolveResolutionID: resolveUnit3DBLUResolutionID,
		resolveCategoryID:   resolveUnit3DBLUCategoryID,
	}
}

func resolveUnit3DBLUCategoryID(meta api.PreparedMetadata) string {
	if strings.Contains(strings.ToUpper(strings.TrimSpace(meta.Edition)), "FANRES") {
		return "3"
	}
	return resolveUnit3DCategoryID(meta)
}

func resolveUnit3DBLUTypeID(meta api.PreparedMetadata) string {
	mapping := map[string]string{
		"DISC":   "1",
		"REMUX":  "3",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
		"ENCODE": "12",
	}
	return mapping[inferUnit3DType(meta)]
}

func resolveUnit3DBLUResolutionID(meta api.PreparedMetadata) string {
	resolution := resolveResolution(meta)
	mapping := map[string]string{
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
	}
	if value, ok := mapping[resolution]; ok {
		return value
	}
	return "10"
}
