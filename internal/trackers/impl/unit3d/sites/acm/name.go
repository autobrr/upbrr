// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string { return buildACMName(meta) }

func buildACMName(meta api.UploadSubject) string {
	name := baseName(meta)
	if name == "" {
		return ""
	}

	title := strings.TrimSpace(resolveACMTitle(meta))
	originalTitle := strings.TrimSpace(resolveACMOriginalTitle(meta))
	if title != "" && originalTitle != "" && !strings.EqualFold(title, originalTitle) {
		name = strings.Replace(name, title, title+" / "+originalTitle+" \u202A", 1)
	}

	audio := strings.TrimSpace(meta.Audio)
	if strings.Contains(audio, "AAC") {
		normalizedAudio := strings.Join(strings.Fields(audio), " ")
		name = strings.Replace(name, normalizedAudio, strings.ReplaceAll(normalizedAudio, "AAC ", "AAC"), 1)
	}
	name = strings.ReplaceAll(name, "DD+ ", "DD+")
	name = strings.ReplaceAll(name, "UHD BluRay REMUX", "Remux")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Remux")
	name = strings.ReplaceAll(name, "H.265", "HEVC")
	name = strings.ReplaceAll(name, " Atmos", "")

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		source := strings.TrimSpace(meta.Source)
		resolution := strings.TrimSpace(unit3d.Resolution(meta))
		if source != "" && resolution != "" {
			name = strings.ReplaceAll(name, source+" DVD5", resolution+" DVD "+source)
			name = strings.ReplaceAll(name, source+" DVD9", resolution+" DVD "+source)
		}
		if audio != "" && audio == strings.TrimSpace(meta.Channels) {
			name = strings.ReplaceAll(name, audio, "MPEG "+audio)
		}
	}

	name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	if unit3d.IsDiscType(meta.DiscType) {
		return name
	}
	return name + acmSubtitleTag(acmSubtitleCodesFor(meta))
}

func resolveACMTitle(meta api.UploadSubject) string {
	for _, value := range []string{
		meta.Release.Title,
		resolveACMTMDBTitle(meta),
		resolveACMIMDBTitle(meta),
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveACMOriginalTitle(meta api.UploadSubject) string {
	for _, value := range []string{
		resolveACMTMDBOriginalTitle(meta),
		resolveACMTMDBRetrievedAKA(meta),
		resolveACMIMDBAKA(meta),
		meta.Release.Alt,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return strings.TrimPrefix(trimmed, "AKA ")
		}
	}
	return ""
}

func acmSubtitleCodesFor(meta api.UploadSubject) []string {
	out := make([]string, 0, len(meta.SubtitleLanguages))
	seen := make(map[string]struct{}, len(meta.SubtitleLanguages))
	for _, language := range meta.SubtitleLanguages {
		key := strings.ToLower(strings.TrimSpace(language))
		if key == "" {
			continue
		}
		code, ok := acmSubtitleCodes[key]
		if !ok {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func acmSubtitleTag(subtitles []string) string {
	if len(subtitles) == 0 {
		return " [No subs]"
	}
	if slices.Contains(subtitles, "Eng") {
		return ""
	}
	if len(subtitles) > 1 {
		return " [No Eng subs]"
	}
	return " [" + subtitles[0] + " subs only]"
}

func resolveACMTMDBTitle(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return meta.ProviderMetadata.TMDB.Title
}

func resolveACMTMDBOriginalTitle(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return meta.ProviderMetadata.TMDB.OriginalTitle
}

func resolveACMTMDBRetrievedAKA(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	return meta.ProviderMetadata.TMDB.RetrievedAKA
}

func resolveACMIMDBTitle(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB == nil {
		return ""
	}
	return meta.ProviderMetadata.IMDB.Title
}

func resolveACMIMDBAKA(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB == nil {
		return ""
	}
	return meta.ProviderMetadata.IMDB.AKA
}

func baseName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

var (
	acmSubtitleCodes = map[string]string{
		"arabic":                "Ara",
		"ara":                   "Ara",
		"ar":                    "Ara",
		"brazilian portuguese":  "Por-BR",
		"brazilian":             "Por-BR",
		"portuguese-br":         "Por-BR",
		"pt-br":                 "Por-BR",
		"bulgarian":             "Bul",
		"bul":                   "Bul",
		"bg":                    "Bul",
		"chinese":               "Chi",
		"chi":                   "Chi",
		"zh":                    "Chi",
		"chinese (simplified)":  "Chi",
		"chinese (traditional)": "Chi",
		"croatian":              "Cro",
		"hrv":                   "Cro",
		"hr":                    "Cro",
		"scr":                   "Cro",
		"czech":                 "Cze",
		"cze":                   "Cze",
		"cz":                    "Cze",
		"cs":                    "Cze",
		"danish":                "Dan",
		"dan":                   "Dan",
		"da":                    "Dan",
		"dutch":                 "Dut",
		"dut":                   "Dut",
		"nl":                    "Dut",
		"english":               "Eng",
		"eng":                   "Eng",
		"en":                    "Eng",
		"english (cc)":          "Eng",
		"english - sdh":         "Eng",
		"english - forced":      "Eng",
		"english (forced)":      "Eng",
		"en (forced)":           "Eng",
		"english intertitles":   "Eng",
		"english (intertitles)": "Eng",
		"english - intertitles": "Eng",
		"en (intertitles)":      "Eng",
		"estonian":              "Est",
		"est":                   "Est",
		"et":                    "Est",
		"finnish":               "Fin",
		"fin":                   "Fin",
		"fi":                    "Fin",
		"french":                "Fre",
		"fre":                   "Fre",
		"fr":                    "Fre",
		"german":                "Ger",
		"ger":                   "Ger",
		"de":                    "Ger",
		"greek":                 "Gre",
		"gre":                   "Gre",
		"el":                    "Gre",
		"hebrew":                "Heb",
		"heb":                   "Heb",
		"he":                    "Heb",
		"hindi":                 "Hin",
		"hin":                   "Hin",
		"hi":                    "Hin",
		"hungarian":             "Hun",
		"hun":                   "Hun",
		"hu":                    "Hun",
		"icelandic":             "Ice",
		"ice":                   "Ice",
		"is":                    "Ice",
		"indonesian":            "Ind",
		"ind":                   "Ind",
		"id":                    "Ind",
		"italian":               "Ita",
		"ita":                   "Ita",
		"it":                    "Ita",
		"japanese":              "Jpn",
		"jpn":                   "Jpn",
		"ja":                    "Jpn",
		"korean":                "Kor",
		"kor":                   "Kor",
		"ko":                    "Kor",
		"latvian":               "Lav",
		"lav":                   "Lav",
		"lv":                    "Lav",
		"lithuanian":            "Lit",
		"lit":                   "Lit",
		"lt":                    "Lit",
		"norwegian":             "Nor",
		"nor":                   "Nor",
		"no":                    "Nor",
		"persian":               "Per",
		"fa":                    "Per",
		"far":                   "Per",
		"polish":                "Pol",
		"pol":                   "Pol",
		"pl":                    "Pol",
		"portuguese":            "Por",
		"por":                   "Por",
		"pt":                    "Por",
		"romanian":              "Rom",
		"rum":                   "Rom",
		"ro":                    "Rom",
		"russian":               "Rus",
		"rus":                   "Rus",
		"ru":                    "Rus",
		"serbian":               "Ser",
		"srp":                   "Ser",
		"sr":                    "Ser",
		"scc":                   "Ser",
		"slovak":                "Slo",
		"slo":                   "Slo",
		"sk":                    "Slo",
		"slovenian":             "Slv",
		"slv":                   "Slv",
		"sl":                    "Slv",
		"spanish":               "Spa",
		"spa":                   "Spa",
		"es":                    "Spa",
		"swedish":               "Swe",
		"swe":                   "Swe",
		"sv":                    "Swe",
		"thai":                  "Tha",
		"tha":                   "Tha",
		"th":                    "Tha",
		"turkish":               "Tur",
		"tur":                   "Tur",
		"tr":                    "Tur",
		"ukrainian":             "Ukr",
		"ukr":                   "Ukr",
		"uk":                    "Ukr",
		"vietnamese":            "Vie",
		"vie":                   "Vie",
		"vi":                    "Vie",
	}
)
