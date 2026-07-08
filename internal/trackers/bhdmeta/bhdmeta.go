// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package bhdmeta maps prepared metadata to BHD upload and dupe-search enums.
package bhdmeta

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// Source maps local source labels to BHD's upload source enum.
// The boolean is false when BHD has no supported source value for the input.
func Source(source string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "BLURAY", "BLU-RAY":
		return "Blu-ray", true
	case "HDDVD", "HD DVD", "HD-DVD":
		return "HD-DVD", true
	case "WEB", "WEB-DL", "WEBDL", "WEBRIP":
		return "WEB", true
	case "HDTV", "UHDTV":
		return "HDTV", true
	case "NTSC", "PAL", "NTSC DVD", "PAL DVD", "DVD":
		return "DVD", true
	default:
		return "", false
	}
}

// SourceForMetadata maps the prepared source, falling back to parsed release
// source when the prepared field is empty.
func SourceForMetadata(meta api.PreparedMetadata) (string, bool) {
	return Source(firstNonEmpty(meta.Source, meta.Release.Source))
}

// Type maps prepared metadata to BHD's upload/search type enum. Unknown or
// unsupported combinations return Other, including direct 576i releases and
// HD-DVD remuxes.
func Type(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		size := 100
		for _, candidate := range []int{25, 50, 66, 100} {
			if meta.SourceSize > 0 && meta.SourceSize < int64(candidate)<<30 {
				size = candidate
				break
			}
		}
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && size != 25 {
			if size == 50 || size == 66 || size == 100 {
				return fmt.Sprintf("UHD %d", size)
			}
			return "Other"
		}
		if size == 25 || size == 50 {
			return fmt.Sprintf("BD %d", size)
		}
		return "Other"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		upper := strings.ToUpper(strings.TrimSpace(meta.Release.Size))
		switch {
		case strings.Contains(upper, "DVD5"):
			return "DVD 5"
		case strings.Contains(upper, "DVD9"):
			return "DVD 9"
		default:
			return "Other"
		}
	}
	if strings.EqualFold(strings.TrimSpace(firstNonEmpty(meta.Type, meta.Release.Type)), "REMUX") {
		source := firstNonEmpty(meta.Source, meta.Release.Source)
		switch {
		case isHDDVDSource(source):
			return "Other"
		case strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD"):
			return "UHD Remux"
		case isDVDSource(source):
			return "DVD Remux"
		case strings.EqualFold(strings.TrimSpace(source), "BluRay"), strings.EqualFold(strings.TrimSpace(source), "Blu-ray"):
			return "BD Remux"
		default:
			return "Other"
		}
	}
	resolution := normalizeResolution(meta.Release.Resolution)
	switch resolution {
	case "2160p", "1080p", "1080i", "720p", "576p", "540p", "480p":
		return resolution
	default:
		return "Other"
	}
}

// SearchType returns the BHD search type filter. The boolean is false for DVD
// searches because BHD matching is broader when the type filter is omitted.
func SearchType(meta api.PreparedMetadata) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "", false
	}
	return Type(meta), true
}

// IsSD reports whether BHD should mark the release as SD or omit HD-only dupe
// filters.
func IsSD(meta api.PreparedMetadata) bool {
	resolution := normalizeResolution(meta.Release.Resolution)
	return strings.Contains(resolution, "480") || strings.Contains(resolution, "540") || strings.Contains(resolution, "576")
}

// IsDVDSource reports whether a source label maps to BHD's DVD source.
func IsDVDSource(source string) bool {
	return isDVDSource(source)
}

func normalizeResolution(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isDVDSource(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	return upper == "PAL DVD" || upper == "NTSC DVD" || upper == "DVD" || upper == "PAL" || upper == "NTSC"
}

func isHDDVDSource(source string) bool {
	upper := strings.ToUpper(strings.TrimSpace(source))
	return upper == "HDDVD" || upper == "HD DVD" || upper == "HD-DVD"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
