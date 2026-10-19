// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package zdf

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func categoryIDs(category api.CanonicalCategory) []string {
	return map[api.CanonicalCategory][]string{
		api.CanonicalCategoryMovie: {"9", "10", "11", "12", "18", "19", "43", "58"},
		api.CanonicalCategoryTV:    {"21", "22", "23", "28", "30", "31", "43", "59"},
	}[category]
}

func isSD(resolution string) bool {
	return resolution == "480p" || resolution == "480i" || resolution == "576p" || resolution == "576i"
}

func categoryID(meta api.UploadSubject) string {
	category := unit3d.Category(meta)
	resolution := unit3d.Resolution(meta)
	mediaType := strings.ToUpper(strings.TrimSpace(unit3d.InferType(meta)))

	if isAnime(meta) {
		return "43"
	}

	if strings.EqualFold(category, "TV") {
		switch {
		case meta.TVPack:
			return "28" // TV/Packs
		case resolution == "2160p" || resolution == "4320p":
			return "22" // TV/4K
		case isSD(resolution):
			return "21" // TV/480p
		case mediaType == "DISC":
			return "23" // TV/Bluray
		case mediaType == "REMUX":
			return "23" // TV/Bluray
		case mediaType == "ENCODE" && isHEVC(meta):
			return "31" // TV/x265
		case mediaType == "ENCODE":
			return "30" // TV/x264
		case strings.EqualFold(meta.Source, "BluRay"):
			return "23" // TV/Bluray
		default:
			return "59" // TV/WEB
		}
	}

	if strings.EqualFold(category, "MOVIE") {
		switch {
		case resolution == "2160p" || resolution == "4320p":
			return "10" // Movies/4K
		case isSD(resolution):
			return "9" // Movies/480p
		case mediaType == "DISC":
			return "12" // Movies/Bluray-Full
		case mediaType == "REMUX":
			return "11" // Movies/Bluray
		case mediaType == "ENCODE" && isHEVC(meta):
			return "19" // Movies/x265
		case mediaType == "ENCODE":
			return "18" // Movies/x264
		case strings.EqualFold(meta.Source, "BluRay"):
			return "11" // Movies/Bluray
		default:
			return "58" // Movies/WEB
		}
	}

	return "58"
}

func isAnime(meta api.UploadSubject) bool {
	return meta.Anime || meta.Identity.MALID != 0
}

func isHEVC(meta api.UploadSubject) bool {
	combined := strings.ToUpper(strings.Join([]string{meta.VideoCodec, meta.VideoEncode}, " "))
	for _, token := range []string{"HEVC", "H.265", "H265", "X265", "265"} {
		if strings.Contains(combined, token) {
			return true
		}
	}
	return false
}
