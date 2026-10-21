// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import "strings"

// measuredVideoEncoder keeps the inspected writing library separate from codec-based naming.
func measuredVideoEncoder(doc mediaInfoDoc) string {
	_, videos, _ := splitMediaInfoTracks(doc)
	if len(videos) != 1 {
		return ""
	}
	library := strings.TrimSpace(trackString(videos[0], "Encoded_Library_Name", "Encoded_Library"))
	if library == "" {
		return ""
	}
	name := strings.ToLower(strings.Fields(library)[0])
	switch name {
	case "x264", "x265":
		return name
	case "svt-av1", "svt-av1-psy", "libaom", "aom", "rav1e":
		return "av1"
	default:
		return library
	}
}
