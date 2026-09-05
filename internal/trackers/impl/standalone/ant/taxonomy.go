// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject, answers map[string]string) (string, int) {
	if text := normalizeTypeName(answers["type"]); text != "" {
		return text, antTypeID(text)
	}
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		runtime := meta.ProviderMetadata.IMDB.RuntimeMinutes
		switch imdbType {
		case "movie", "tv movie", "tvmovie":
			if runtime >= 45 || runtime == 0 {
				return "Feature Film", 0
			}
			return "Short Film", 1
		case "short":
			return "Short Film", 1
		case "tv mini series":
			return "Miniseries", 2
		case "comedy":
			return "Other", 3
		}
	}
	keywords := strings.ToLower(strings.TrimSpace(resolveKeywords(meta)))
	category := strings.ToLower(strings.TrimSpace(string(meta.Identity.Category)))
	if category == "movie" {
		runtime := 0
		if meta.ProviderMetadata.TMDB != nil {
			runtime = meta.ProviderMetadata.TMDB.Runtime
		}
		if runtime >= 45 || runtime == 0 {
			return "Feature Film", 0
		}
		return "Short Film", 1
	}
	if strings.Contains(keywords, "miniseries") {
		return "Miniseries", 2
	}
	if strings.Contains(keywords, "short") || strings.Contains(keywords, "short film") {
		return "Short Film", 1
	}
	if strings.Contains(keywords, "stand-up comedy") {
		return "Other", 3
	}
	return "", 0
}

func normalizeTypeName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "feature film", "feature", "movie":
		return "Feature Film"
	case "short film", "short":
		return "Short Film"
	case "miniseries", "mini series", "mini-series":
		return "Miniseries"
	case "other", "comedy":
		return "Other"
	default:
		return ""
	}
}

func resolveAudioFormat(meta api.UploadSubject) string {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	switch {
	case audio == "":
		return "NoAudio"
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "EAC3"):
		return "EAC3"
	case strings.Contains(audio, " DD "), strings.HasPrefix(audio, "DD"), strings.Contains(audio, "AC3"):
		return "AC3"
	case strings.Contains(audio, "DTS-HD MA"), strings.Contains(audio, "DTS MA"), strings.Contains(audio, "DTS:X"), strings.Contains(audio, "DTS-HD HRA"):
		return "DTSMA"
	case strings.Contains(audio, "DTS"):
		return "DTS"
	case strings.Contains(audio, "TRUEHD"):
		return "TrueHD"
	case strings.Contains(audio, "FLAC"):
		return "FLAC"
	case strings.Contains(audio, "PCM"):
		return "PCM"
	case strings.Contains(audio, "OPUS"):
		return "Opus"
	case strings.Contains(audio, "AAC"):
		return "AAC"
	case strings.Contains(audio, "MP3"):
		return "MP3"
	case strings.Contains(audio, "MP2"):
		return "MP2"
	default:
		return "Other"
	}
}

func resolveFlags(meta api.UploadSubject) []string {
	flags := make([]string, 0, 12)
	edition := strings.ReplaceAll(meta.Edition, "'", "")
	for _, candidate := range []string{"Directors", "Extended", "Uncut", "Unrated", "4KRemaster", "IMAX"} {
		if strings.Contains(edition, candidate) {
			flags = append(flags, candidate)
		}
	}
	if strings.Contains(meta.Audio, "Dual-Audio") {
		flags = append(flags, "DualAudio")
	}
	if strings.Contains(meta.Audio, "Atmos") {
		flags = append(flags, "Atmos")
	}
	if meta.HasCommentary {
		flags = append(flags, "Commentary")
	}
	if strings.EqualFold(strings.TrimSpace(meta.Is3D), "3D") {
		flags = append(flags, "3D")
	}
	if strings.Contains(strings.ToUpper(meta.HDR), "HDR") {
		flags = append(flags, "HDR10")
	}
	if strings.Contains(strings.ToUpper(meta.HDR), "DV") {
		flags = append(flags, "DV")
	}
	if strings.Contains(strings.ToUpper(meta.Distributor), "CRITERION") || strings.Contains(strings.ToUpper(meta.Edition), "CRITERION") {
		flags = append(flags, "Criterion")
	}
	if strings.Contains(strings.ToUpper(meta.Type), "REMUX") {
		flags = append(flags, "Remux")
	}
	return dedupeStrings(flags)
}

func resolveTags(meta api.UploadSubject, answers map[string]string) (string, bool) {
	if tagValue := normalizeTags(strings.TrimSpace(answers["tags"])); tagValue != "" {
		return tagValue, true
	}
	values := make([]string, 0, 8)
	if meta.ProviderMetadata.TMDB != nil {
		values = append(values, splitTags(meta.ProviderMetadata.TMDB.Genres)...)
	}
	if len(values) == 0 {
		if meta.ProviderMetadata.IMDB != nil && len(splitTags(meta.ProviderMetadata.IMDB.Genres)) > 0 {
			return "", true
		}
		return "", true
	}
	allowed := map[string]struct{}{
		"action":      {},
		"adventure":   {},
		"animation":   {},
		"comedy":      {},
		"crime":       {},
		"documentary": {},
		"drama":       {},
		"family":      {},
		"fantasy":     {},
		"history":     {},
		"horror":      {},
		"music":       {},
		"mystery":     {},
		"romance":     {},
		"sci.fi":      {},
		"thriller":    {},
		"war":         {},
		"western":     {},
	}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; ok {
			filtered = append(filtered, value)
		}
	}
	return strings.Join(dedupeStrings(filtered), ","), false
}

func detectAdult(meta api.UploadSubject) bool {
	candidates := []string{meta.Release.Genre, resolveKeywords(meta)}
	if meta.ProviderMetadata.TMDB != nil {
		candidates = append(candidates, meta.ProviderMetadata.TMDB.Genres)
	}
	for _, candidate := range candidates {
		lower := strings.ToLower(candidate)
		for _, token := range []string{"xxx", "erotic", "porn", "adult", "orgy"} {
			if strings.Contains(lower, token) {
				return true
			}
		}
	}
	return false
}

func resolveAdultScreensAllowed(answers map[string]string, adultContent bool) bool {
	if !adultContent {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(answers["adult_screens"])) {
	case "y", "yes", "true", "1":
		return true
	default:
		return false
	}
}

func antTypeID(value string) int {
	switch normalizeTypeName(value) {
	case "Short Film":
		return 1
	case "Miniseries":
		return 2
	case "Other":
		return 3
	default:
		return 0
	}
}

func normalizeTags(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return strings.Join(dedupeStrings(splitTags(value)), ",")
}

func splitTags(value string) []string {
	items := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	result := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(item, " ", ".")))
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}
