// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import "github.com/autobrr/upbrr/pkg/api"

func siteFNPProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveResolutionID: resolveUnit3DFNPResolutionID,
	}
}

func resolveUnit3DFNPResolutionID(meta api.PreparedMetadata) string {
	resolution := resolveResolution(meta)
	mapping := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "11",
		"720p":  "5",
		"576p":  "6",
		"576i":  "15",
		"480p":  "8",
		"480i":  "14",
	}
	if value, ok := mapping[resolution]; ok {
		return value
	}
	return "10"
}
