// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject) string {
	category := strings.ToLower(strings.TrimSpace(string(meta.Identity.Category)))
	if category == "" {
		category = strings.ToLower(strings.TrimSpace(string(meta.Identity.Category)))
	}
	if meta.ProviderMetadata.IMDB != nil && strings.Contains(strings.ToLower(meta.ProviderMetadata.IMDB.Type), "concert") {
		return "Music"
	}
	if meta.ProviderMetadata.TMDB != nil &&
		(strings.Contains(strings.ToLower(meta.ProviderMetadata.TMDB.Genres), "documentary") || strings.Contains(strings.ToLower(meta.ProviderMetadata.TMDB.Keywords), "documentary")) {
		return "Documentary"
	}
	if category == "movie" {
		return "Feature Film"
	}
	if category == "tv" {
		if meta.TVPack {
			return "Miniseries"
		}
		return "Short Film"
	}
	return "Feature Film"
}

func resolveCodec(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "BD50"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "DVD9"
	}
	codec := strings.TrimSpace(meta.VideoCodec)
	if codec == "" {
		codec = strings.TrimSpace(meta.VideoEncode)
	}
	replacer := strings.NewReplacer("AVC", "H.264", "HEVC", "H.265")
	codec = replacer.Replace(codec)
	if meta.HasEncodeSettings {
		codec = strings.ReplaceAll(codec, "H.", "x")
	}
	if codec == "" {
		return "Other"
	}
	return codec
}

func resolveResolution(meta api.UploadSubject) (string, string) {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		resolution = "Other"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		source := strings.TrimSpace(meta.Source)
		source = strings.ReplaceAll(source, " DVD", "")
		if source != "" {
			return source, ""
		}
	}
	if strings.EqualFold(resolution, "OTHER") {
		return "Other", "Other"
	}
	return resolution, ""
}

func resolveContainer(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "m2ts"
	case "DVD":
		return "VOB IFO"
	default:
		switch strings.ToLower(filepath.Ext(firstFile(meta))) {
		case ".mkv":
			return "MKV"
		case ".mp4":
			return "MP4"
		default:
			return "Other"
		}
	}
}

func resolveSource(source string) string {
	switch strings.TrimSpace(source) {
	case "Blu-ray", "BluRay":
		return "Blu-ray"
	case "HD DVD", "HDDVD":
		return "HD-DVD"
	case "Web":
		return "WEB"
	case "HDTV", "UHDTV":
		return "HDTV"
	case "NTSC", "PAL":
		return "DVD"
	default:
		return "OtherR"
	}
}

func resolveSubtitles(meta api.UploadSubject) []int {
	if len(meta.SubtitleLanguages) == 0 {
		return []int{44}
	}
	ids := make([]int, 0, len(meta.SubtitleLanguages))
	seen := make(map[int]struct{})
	for _, language := range meta.SubtitleLanguages {
		if value, ok := subtitleIDs[strings.ToLower(strings.TrimSpace(language))]; ok {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			ids = append(ids, value)
		}
	}
	if len(ids) == 0 {
		return []int{44}
	}
	return ids
}

func resolveTags(meta api.UploadSubject) string {
	values := make([]string, 0, 8)
	if meta.ProviderMetadata.TMDB != nil {
		for item := range strings.SplitSeq(meta.ProviderMetadata.TMDB.Genres, ",") {
			trimmed := strings.ToLower(strings.TrimSpace(item))
			if trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	if len(values) == 0 && strings.TrimSpace(meta.Release.Genre) != "" {
		for item := range strings.SplitSeq(meta.Release.Genre, ",") {
			trimmed := strings.ToLower(strings.TrimSpace(item))
			if trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	if len(values) == 0 {
		values = append(values, "action")
	}
	seen := make(map[string]struct{}, len(values))
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		filtered = append(filtered, value)
	}
	return strings.Join(filtered, ", ")
}

var subtitleIDs = map[string]int{
	"arabic":               22,
	"brazilian portuguese": 49,
	"bulgarian":            29,
	"chinese":              14,
	"croatian":             23,
	"czech":                30,
	"danish":               10,
	"dutch":                9,
	"english":              3,
	"english - forced":     50,
	"english forced":       50,
	"english intertitles":  51,
	"estonian":             38,
	"finnish":              15,
	"french":               5,
	"german":               6,
	"greek":                26,
	"hebrew":               40,
	"hindi":                41,
	"hungarian":            24,
	"icelandic":            28,
	"indonesian":           47,
	"italian":              16,
	"japanese":             8,
	"korean":               19,
	"latvian":              37,
	"lithuanian":           39,
	"norwegian":            12,
	"polish":               17,
	"portuguese":           21,
	"romanian":             13,
	"russian":              7,
	"serbian":              31,
	"slovak":               42,
	"slovenian":            43,
	"spanish":              4,
	"swedish":              11,
	"thai":                 20,
	"turkish":              18,
	"ukrainian":            34,
	"vietnamese":           25,
}
