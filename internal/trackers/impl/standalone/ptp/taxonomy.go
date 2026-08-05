// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject) string {
	category := strings.ToLower(strings.TrimSpace(string(meta.Identity.Category)))
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		switch {
		case strings.Contains(imdbType, "concert"):
			return "Live Performance"
		case strings.Contains(imdbType, "short"):
			return "Short Film"
		case strings.Contains(imdbType, "mini series"), strings.Contains(imdbType, "miniseries"):
			return "Miniseries"
		case strings.Contains(imdbType, "stand-up"), strings.Contains(imdbType, "stand up"):
			return "Stand-up Comedy"
		}
	}
	if meta.ProviderMetadata.TMDB != nil {
		keywords := strings.ToLower(meta.ProviderMetadata.TMDB.Keywords)
		switch {
		case strings.Contains(keywords, "concert"):
			return "Live Performance"
		case strings.Contains(keywords, "stand-up comedy"), strings.Contains(keywords, "stand up comedy"):
			return "Stand-up Comedy"
		case strings.Contains(keywords, "miniseries"), strings.Contains(keywords, "mini-series"):
			return "Miniseries"
		case strings.Contains(keywords, "short film"):
			return "Short Film"
		}
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
		switch {
		case meta.SourceSize <= 0:
			return "BD50"
		case meta.SourceSize <= 2328*(1<<30)/100:
			return "BD25"
		case meta.SourceSize <= 4657*(1<<30)/100:
			return "BD50"
		case meta.SourceSize <= 6147*(1<<30)/100:
			return "BD66"
		default:
			return "BD100"
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		if meta.SourceSize > 0 && meta.SourceSize <= 437*(1<<30)/100 {
			return "DVD5"
		}
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

func resolveResolution(meta api.UploadSubject) (string, string, string) {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		source := strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(meta.Source)), " DVD")
		if source == "NTSC" || source == "PAL" {
			return source, "", ""
		}
	}
	if resolution == "" {
		for token := range strings.FieldsSeq(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(meta.ReleaseName + " " + meta.Filename)) {
			switch strings.ToLower(token) {
			case "480i", "480p", "540p", "576i", "576p", "720p", "1080i", "1080p", "1440p", "2160p", "4320p", "8640p":
				resolution = token
			}
		}
	}
	switch strings.ToLower(resolution) {
	case "ntsc":
		return "NTSC", "", ""
	case "pal":
		return "PAL", "", ""
	case "480p", "576p", "720p", "1080i", "1080p", "2160p":
		return strings.ToLower(resolution), "", ""
	}
	if width, height, ok := ptpOtherResolution(resolution); ok {
		return "Other", width, height
	}
	return "Other", "", ""
}

func ptpOtherResolution(resolution string) (string, string, bool) {
	if dimensions, ok := map[string][2]string{
		"480i":  {"720", "480"},
		"540p":  {"960", "540"},
		"576i":  {"720", "576"},
		"1440p": {"2560", "1440"},
		"4320p": {"7680", "4320"},
		"8640p": {"15360", "8640"},
	}[strings.ToLower(strings.TrimSpace(resolution))]; ok {
		return dimensions[0], dimensions[1], true
	}
	width, height, ok := strings.Cut(strings.ToLower(strings.TrimSpace(resolution)), "x")
	if !ok {
		return "", "", false
	}
	parsedWidth, widthErr := strconv.Atoi(width)
	parsedHeight, heightErr := strconv.Atoi(height)
	if widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0 {
		return "", "", false
	}
	return strconv.Itoa(parsedWidth), strconv.Itoa(parsedHeight), true
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
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "BLU-RAY", "BLURAY":
		return "Blu-ray"
	case "HD DVD", "HDDVD", "HD-DVD":
		return "HD-DVD"
	case "WEB", "WEB-DL", "WEBDL":
		return "WEB"
	case "HDTV", "UHDTV":
		return "HDTV"
	case "NTSC", "PAL", "DVD":
		return "DVD"
	case "TV":
		return "TV"
	case "VHS":
		return "VHS"
	default:
		return "Other"
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
			if tag := ptpTag(item); tag != "" {
				values = append(values, tag)
			}
		}
	}
	if len(values) == 0 && strings.TrimSpace(meta.Release.Genre) != "" {
		for item := range strings.SplitSeq(meta.Release.Genre, ",") {
			if tag := ptpTag(item); tag != "" {
				values = append(values, tag)
			}
		}
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

func ptpTag(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "science fiction", "sci-fi", "sci fi":
		return "sci.fi"
	case "martial arts":
		return "martial.arts"
	case "film noir":
		return "film.noir"
	case "music":
		return "musical"
	case "war & politics", "war and politics":
		return "war"
	}
	for _, allowed := range []string{
		"action", "adventure", "animation", "arthouse", "asian", "biography", "camp", "comedy", "crime", "cult", "documentary",
		"drama", "experimental", "exploitation", "family", "fantasy", "film.noir", "history", "horror", "martial.arts", "musical", "mystery",
		"performance", "philosophy", "politics", "romance", "sci.fi", "short", "silent", "sport", "thriller", "video.art", "war", "western",
	} {
		if normalized == allowed {
			return allowed
		}
	}
	return ""
}

func resolveTrumpable(meta api.UploadSubject) []int {
	values := make([]int, 0, 2)
	if hasHardcodedSubtitles(meta) {
		values = append(values, 4)
	}
	if len(meta.AudioLanguages) > 0 && !ptpEnglishLanguage(meta.AudioLanguages[0]) && !ptpHasEnglishLanguage(meta.SubtitleLanguages) {
		values = append(values, 14)
	}
	return values
}

func hasHardcodedSubtitles(meta api.UploadSubject) bool {
	name := strings.ToLower(strings.Join(append([]string{
		meta.ReleaseName,
		meta.ReleaseNameNoTag,
		meta.Filename,
	}, meta.FileList...), " "))
	return strings.Contains(name, "hardsub") || strings.Contains(name, "hard-sub") || strings.Contains(name, "hardcoded")
}

func withHardcodedSubtitleLanguages(meta api.UploadSubject, value string) (api.UploadSubject, error) {
	if !hasHardcodedSubtitles(meta) {
		return meta, nil
	}
	if strings.TrimSpace(value) == "" {
		return api.UploadSubject{}, errors.New("trackers: PTP hardcoded subtitle languages are required")
	}
	meta.SubtitleLanguages = append([]string(nil), meta.SubtitleLanguages...)
	for language := range strings.SplitSeq(value, ",") {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		if _, ok := subtitleIDs[strings.ToLower(language)]; !ok {
			return api.UploadSubject{}, fmt.Errorf("trackers: PTP unsupported hardcoded subtitle language %q", language)
		}
		meta.SubtitleLanguages = append(meta.SubtitleLanguages, language)
	}
	return meta, nil
}

func ptpHasEnglishLanguage(values []string) bool {
	return slices.ContainsFunc(values, ptpEnglishLanguage)
}

func ptpEnglishLanguage(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized == "en" || normalized == "eng" || strings.HasPrefix(normalized, "english")
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
	"malay":                54,
	"norwegian":            12,
	"persian":              52,
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
	"welsh":                55,
}
