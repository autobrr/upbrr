// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveContainer(meta api.UploadSubject) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "5"
	case "DVD":
		return "15"
	}
	ext := strings.ToLower(strings.TrimSpace(meta.Container))
	if ext == "" {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(metautil.FirstNonEmptyTrimmed(meta.VideoPath, meta.SourcePath)), "."))
	}
	switch ext {
	case "mkv":
		return "6"
	case "mp4":
		return "8"
	default:
		return ""
	}
}

func resolveQuality(meta api.UploadSubject) string {
	if !strings.EqualFold(meta.Type, "DISC") {
		switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
		case "ENCODE":
			return "9"
		case "REMUX":
			return "39"
		case "WEBDL":
			return "23"
		case "WEBRIP":
			return "38"
		case "BDRIP":
			return "8"
		case "DVDRIP":
			return "3"
		default:
			return "0"
		}
	}
	if strings.EqualFold(meta.DiscType, "DVD") {
		if meta.SourceSize > 7_500_000_000 {
			return "46"
		}
		return "45"
	}
	if strings.EqualFold(meta.DiscType, "HDDVD") {
		return "15"
	}
	switch size := meta.SourceSize; {
	case size > 66<<30:
		return "43"
	case size > 50<<30:
		return "42"
	case size > 25<<30:
		return "41"
	default:
		return "40"
	}
}

func resolveVideoCodec(meta api.UploadSubject) string {
	codec := strings.ToUpper(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.VideoEncode, meta.VideoCodec)))
	codecClean := codec
	if strings.Contains(codec, "264") {
		codecClean = "H264"
	} else if strings.Contains(codec, "265") {
		codecClean = "HEVC"
	}
	switch {
	case strings.Contains(strings.ToUpper(meta.HDR), "HDR") && (codecClean == "HEVC" || codecClean == "H265"):
		return "28"
	case strings.Contains(strings.ToUpper(meta.HDR), "HDR") && (codecClean == "AVC" || codecClean == "H264"):
		return "32"
	case strings.Contains(codec, "AV1"):
		return "29"
	case strings.Contains(codec, "HEVC"):
		return "27"
	case strings.Contains(codec, "H265"):
		return "18"
	case strings.Contains(codec, "AVC"):
		return "30"
	case strings.Contains(codec, "H264"):
		return "17"
	case strings.Contains(codec, "VC-1"):
		return "21"
	case strings.Contains(codec, "MPEG-2"):
		return "11"
	default:
		return "16"
	}
}

func resolveAudioCodec(meta api.UploadSubject) string {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	switch {
	case strings.Contains(audio, "ATMOS"):
		return "43"
	case strings.Contains(audio, "DTS:X"):
		return "25"
	case strings.Contains(audio, "DTS-HD MA"):
		return "24"
	case strings.Contains(audio, "DTS-HD"):
		return "23"
	case strings.Contains(audio, "TRUEHD"):
		return "29"
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "E-AC-3"):
		return "26"
	case strings.Contains(audio, "DD"), strings.Contains(audio, "AC3"):
		return "11"
	case strings.Contains(audio, "DTS"):
		return "12"
	case strings.Contains(audio, "FLAC"):
		return "13"
	case strings.Contains(audio, "LPCM"):
		return "21"
	case strings.Contains(audio, "PCM"):
		return "28"
	case strings.Contains(audio, "AAC"):
		return "10"
	case strings.Contains(audio, "OPUS"):
		return "27"
	case strings.Contains(audio, "MPEG"):
		return "17"
	default:
		return "20"
	}
}

func resolveAudio(meta api.UploadSubject) string {
	original := strings.ToLower(strings.TrimSpace(resolveOriginalLanguage(meta)))
	audioLangs := lowerStrings(meta.AudioLanguages)
	hasPTAudio := containsAny(audioLangs, []string{"portuguese", "português", "pt"})
	hasPTSubs := resolveSubtitle(meta) == "Embutida"
	isOriginalPT := containsAny([]string{original}, []string{"portuguese", "português", "pt"})
	switch {
	case hasPTAudio && isOriginalPT:
		return "4"
	case hasPTAudio && countNonPortuguese(audioLangs) > 0:
		return "2"
	case hasPTAudio:
		return "3"
	case hasPTSubs:
		return "1"
	default:
		return "7"
	}
}

func resolveSubtitle(meta api.UploadSubject) string {
	if containsAny(lowerStrings(meta.SubtitleLanguages), []string{"portuguese", "português", "pt", "brazilian portuguese"}) {
		return "Embutida"
	}
	return "S_legenda"
}

func resolveLanguage(meta api.UploadSubject) string {
	return mapLanguage(resolveOriginalLanguage(meta), map[string]string{
		"bg": "15",
		"da": "12",
		"de": "3",
		"en": "1",
		"es": "6",
		"fi": "14",
		"fr": "2",
		"hi": "23",
		"it": "4",
		"ja": "5",
		"ko": "20",
		"nl": "17",
		"no": "16",
		"pl": "19",
		"pt": "8",
		"ru": "7",
		"sv": "13",
		"th": "21",
		"tr": "25",
		"zh": "10",
	}, "11")
}

func resolveAnimeLanguage(meta api.UploadSubject) string {
	return mapLanguage(resolveOriginalLanguage(meta), map[string]string{
		"de": "3",
		"en": "4",
		"es": "1",
		"ja": "8",
		"ko": "11",
		"pt": "5",
		"ru": "2",
		"zh": "9",
	}, "6")
}

func resolveAnimeAudioLanguage(meta api.UploadSubject) string {
	if audio := resolveAudio(meta); audio == "2" || audio == "3" || audio == "4" {
		return "8"
	}
	return resolveLanguage(meta)
}

func resolveAnimeType(meta api.UploadSubject) string {
	if categoryOf(meta) == "TV" {
		return "118"
	}
	return "116"
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func boolFlag(ok bool) string {
	if ok {
		return "1"
	}
	return "2"
}

func mapLanguage(value string, mappings map[string]string, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	if mapped, ok := mappings[key]; ok {
		return mapped
	}
	return fallback
}

func lowerStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, strings.ToLower(strings.TrimSpace(value)))
	}
	return out
}

func countNonPortuguese(values []string) int {
	count := 0
	for _, value := range values {
		if !containsAny([]string{value}, []string{"portuguese", "português", "pt"}) {
			count++
		}
	}
	return count
}

func containsAny(values []string, targets []string) bool {
	for _, value := range values {
		for _, target := range targets {
			if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
				return true
			}
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
