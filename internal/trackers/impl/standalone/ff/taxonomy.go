// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveTypeID(meta api.UploadSubject) string {
	if meta.Anime {
		return "44"
	}
	if strings.EqualFold(categoryOf(meta), "TV") {
		return "7"
	}
	return "19"
}

func resolveAnimeVCodec(meta api.UploadSubject) string {
	if strings.Contains(strings.ToLower(meta.VideoCodec), "vc-1") {
		return "VC1"
	}
	if strings.Contains(strings.ToLower(meta.VideoEncode), "h.264") {
		return "h264"
	}
	return "x264"
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}

func isSD(meta api.UploadSubject) bool {
	return strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "480p") || strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "576p")
}

func resolveMovieType(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.Source), "DVD") {
		return "DVDR"
	}
	if strings.Contains(strings.ToLower(meta.VideoCodec), "hevc") || strings.Contains(strings.ToLower(meta.VideoEncode), "265") {
		return "x265"
	}
	return "x264"
}

func resolveTVType(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.Source), "DVD") {
		return "DVDR"
	}
	if strings.Contains(strings.ToLower(meta.Source), "web") {
		if isSD(meta) {
			return "Web-SD"
		}
		return "Web-HD"
	}
	if strings.Contains(strings.ToLower(meta.VideoCodec), "hevc") || strings.Contains(strings.ToLower(meta.VideoEncode), "265") {
		if isSD(meta) {
			return "x265-SD"
		}
		return "x265-HD"
	}
	if isSD(meta) {
		return "x264-SD"
	}
	return "x264-HD"
}

func resolveAnimeType(meta api.UploadSubject) string {
	if meta.SeasonInt == 0 {
		return "TVSpecial"
	}
	if strings.EqualFold(categoryOf(meta), "MOVIE") {
		return "Movie"
	}
	return "TVSeries"
}

func resolveMovieSource(meta api.UploadSubject) string {
	switch strings.ToLower(strings.TrimSpace(meta.Source)) {
	case "dvd":
		return "DVD"
	case "blu-ray", "bluray":
		return "BluRay"
	case "hdtv":
		return "HDTV"
	case "webrip", "webdl", "web":
		return "WebRIP"
	default:
		return "BluRay"
	}
}

func resolveTVSource(meta api.UploadSubject) string {
	switch strings.ToLower(strings.TrimSpace(meta.Source)) {
	case "dvd":
		return "DVD"
	case "blu-ray", "bluray":
		return "BluRay"
	case "hdtv":
		return "HDTV"
	case "webrip", "webdl", "web":
		return "WebRIP"
	default:
		return "HDTV"
	}
}

func resolveAnimeSource(meta api.UploadSubject) string {
	switch strings.ToLower(strings.TrimSpace(meta.Source)) {
	case "dvd":
		return "DVD"
	case "blu-ray", "bluray":
		return "BluRay"
	default:
		return "Anime Series"
	}
}
