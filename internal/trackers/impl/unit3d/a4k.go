// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import "github.com/autobrr/upbrr/pkg/api"

func siteA4KProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveTypeID:       resolveUnit3DA4KTypeID,
		resolveResolutionID: resolveUnit3DA4KResolutionID,
	}
}

func resolveUnit3DA4KTypeID(meta api.PreparedMetadata) string {
	mapping := map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"ENCODE": "3",
	}
	return mapping[inferUnit3DType(meta)]
}

func resolveUnit3DA4KResolutionID(meta api.PreparedMetadata) string {
	resolution := resolveResolution(meta)
	mapping := map[string]string{
		"4320p": "1",
		"2160p": "2",
	}
	if value, ok := mapping[resolution]; ok {
		return value
	}
	return "10"
}
