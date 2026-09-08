// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) string {
	if categoryOf(meta) == "TV" {
		if meta.TVPack {
			return "12"
		}
		return "10"
	}
	return "7"
}

func resolveSubcategoryID(meta api.UploadSubject) string {
	if categoryOf(meta) != "TV" {
		return resolveMovieSubcategory(meta)
	}
	if meta.TVPack {
		return resolveTVPackSubcategory(meta.Type)
	}
	return resolveTVEpisodeSubcategory(meta.Type)
}

func resolveMovieSubcategory(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "DISC":
		if meta.Is3D != "" {
			return "46"
		}
		return "2"
	case "REMUX":
		switch {
		case hasH265Video(meta):
			return "48"
		case meta.Is3D != "":
			return "45"
		default:
			return "2"
		}
	case "HDTV":
		return "6"
	case "ENCODE":
		switch {
		case hasH265Video(meta):
			return "43"
		case meta.Is3D != "":
			return "44"
		default:
			return "1"
		}
	case "WEBDL", "WEBRIP":
		return "5"
	default:
		return "0"
	}
}

func resolveTVEpisodeSubcategory(typeValue string) string {
	switch strings.ToUpper(strings.TrimSpace(typeValue)) {
	case "HDTV":
		return "7"
	case "WEBDL", "WEBRIP":
		return "8"
	case "ENCODE":
		return "10"
	case "REMUX":
		return "11"
	case "DISC":
		return "12"
	default:
		return "0"
	}
}

func resolveTVPackSubcategory(typeValue string) string {
	switch strings.ToUpper(strings.TrimSpace(typeValue)) {
	case "HDTV":
		return "13"
	case "WEBDL":
		return "14"
	case "WEBRIP":
		return "8"
	case "ENCODE":
		return "16"
	case "REMUX":
		return "17"
	case "DISC":
		return "18"
	default:
		return "0"
	}
}

func resolveResolutionID(meta api.UploadSubject) string {
	switch normalizeResolution(meta.Release.Resolution) {
	case "2160P":
		return "4"
	case "1080P":
		return "3"
	case "1080I":
		return "2"
	case "720P":
		return "1"
	default:
		return "10"
	}
}

func hasH265Video(meta api.UploadSubject) bool {
	for _, value := range []string{meta.VideoEncode, meta.VideoCodec} {
		normalized := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
		if normalized == "H265" || normalized == "X265" || normalized == "HEVC" {
			return true
		}
	}
	return false
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func normalizeResolution(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	for _, candidate := range []string{"2160P", "1080P", "1080I", "720P"} {
		if strings.Contains(upper, candidate) {
			return candidate
		}
	}
	return upper
}
