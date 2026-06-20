// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import "github.com/autobrr/upbrr/pkg/api"

// siteZNTHProfile registers ZNTH's custom Unit3D type-id resolver.
func siteZNTHProfile() unit3DSiteProfile {
	return unit3DSiteProfile{resolveTypeID: resolveUnit3DZNTHTypeID}
}

// resolveUnit3DZNTHTypeID maps inferred release types to ZNTH type_id values.
// Unsupported or unknown types return an empty id for the shared Unit3D resolver
// to reject.
func resolveUnit3DZNTHTypeID(meta api.PreparedMetadata) string {
	mapping := map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"ENCODE": "3",
		"DVDRIP": "11",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
	}
	return mapping[inferUnit3DType(meta)]
}
