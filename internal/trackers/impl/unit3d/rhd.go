// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"github.com/autobrr/upbrr/pkg/api"
)

func siteRHDProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveResolutionID: resolveUnit3DRHDResolutionID,
	}
}

func resolveUnit3DRHDResolutionID(meta api.PreparedMetadata) string {
	resolution := resolveResolution(meta)
	mapping := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "12",
		"576i":  "13",
		"480p":  "11",
		"480i":  "18",
	}
	if value, ok := mapping[resolution]; ok {
		return value
	}
	return "10"
}
