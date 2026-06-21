// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"regexp"
	"strings"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

var noGroupTagPattern = regexp.MustCompile(`(?i)-(nogrp|nogroup|unknown|-unk-)`)
var (
	languageTagLookupOnce sync.Once
	languageTagLookup     map[string]language.Tag
)

func buildUnit3DName(tracker string, meta api.PreparedMetadata, cfg config.TrackerConfig) string {
	trackerName := strings.ToUpper(strings.TrimSpace(tracker))
	if trackerName == "RHD" {
		return buildRHDName(meta)
	}

	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}

	switch trackerName {
	case "AITHER":
		return BuildAitherName(meta)
	case "ACM":
		return buildACMName(meta)
	case "CBR":
		return BuildCBRName(meta, cfg.TagForCustomRelease)
	case "DP":
		return buildDPName(name, meta)
	case "LCD":
		return BuildCBRName(meta, cfg.TagForCustomRelease)
	case "LDU":
		return buildLDUName(name, meta)
	case "RF":
		return addNoGroupSuffix(name, meta, "NoGroup")
	case "SAM":
		return BuildCBRName(meta, cfg.TagForCustomRelease)
	case "OE":
		return addNoGroupSuffix(name, meta, "NOGRP")
	case "ULCX":
		return buildULCXName(name, meta)
	default:
		return name
	}
}

func baseReleaseName(meta api.PreparedMetadata) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func buildDPName(name string, meta api.PreparedMetadata) string {
	audioLabel := resolveDPAudioLabel(meta.AudioLanguages)
	if audioLabel != "" {
		name = strings.Replace(name, "Dual-Audio", audioLabel, 1)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func buildULCXName(name string, meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && (strings.Contains(strings.ToLower(strings.TrimSpace(meta.Edition)), "hybrid") || meta.WebDV) {
		name = strings.Replace(name, "Hybrid ", "", 1)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func resolveDPAudioLabel(languages []string) string {
	normalized := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		trimmed := strings.TrimSpace(language)
		if trimmed == "" {
			continue
		}
		normalized[strings.ToUpper(trimmed)] = struct{}{}
	}

	switch len(normalized) {
	case 0:
		return ""
	case 1:
		for value := range normalized {
			return value
		}
		return ""
	case 2:
		return "Dual-Audio"
	default:
		return "MULTi"
	}
}

func addNoGroupSuffix(name string, meta api.PreparedMetadata, suffix string) string {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	normalizedName := noGroupTagPattern.ReplaceAllString(name, "")
	normalizedName = strings.TrimSpace(strings.Join(strings.Fields(normalizedName), " "))
	if tag != "" && !isNoGroupTag(tag) {
		return normalizedName
	}
	if normalizedName == "" {
		return normalizedName
	}
	if strings.HasSuffix(strings.ToUpper(normalizedName), "-"+strings.ToUpper(suffix)) {
		return normalizedName
	}
	return normalizedName + "-" + suffix
}

func buildLDUName(name string, meta api.PreparedMetadata) string {
	catID := resolveUnit3DLDUCategoryID(meta)
	nonEnglishOriginal := !isEnglishLanguageToken(resolveOriginalLanguage(meta))
	isoAudio, nonEnglishAudio := firstAudioLanguageCode(meta.AudioLanguages)
	isoSubtitle := firstSubtitleLanguageCode(meta.SubtitleLanguages)

	if catID == "18" && isoSubtitle != "" {
		return strings.TrimSpace(strings.Join(strings.Fields(name+" [Subs "+isoSubtitle+"]"), " "))
	}

	if !nonEnglishOriginal && !nonEnglishAudio {
		return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	}

	languageParts := make([]string, 0, 2)
	if isoAudio != "" {
		languageParts = append(languageParts, "["+isoAudio+"]")
	}
	if isoSubtitle != "" {
		languageParts = append(languageParts, "[Subs "+isoSubtitle+"]")
	}
	if len(languageParts) == 0 {
		return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	}

	return strings.TrimSpace(strings.Join(strings.Fields(name+" "+strings.Join(languageParts, " ")), " "))
}

func firstAudioLanguageCode(languages []string) (string, bool) {
	for _, value := range languages {
		code, english, ok := languageCode(value)
		if ok {
			return code, !english
		}
	}
	return "", false
}

func firstSubtitleLanguageCode(languages []string) string {
	for _, value := range languages {
		code, _, ok := languageCode(value)
		if ok {
			return code
		}
	}
	return ""
}

func languageCode(value string) (string, bool, bool) {
	normalized := languageutil.NormalizeLanguageDisplay(value)
	if normalized == "" {
		normalized = strings.TrimSpace(value)
	}
	tag, ok := parseLanguageTag(normalized)
	if !ok {
		return "", false, false
	}
	base, _ := tag.Base()
	if base.String() == "und" {
		return "", false, false
	}
	code := base.ISO3()
	if code == "" {
		return "", false, false
	}
	return strings.ToUpper(code), isEnglishLanguageTag(base.String()), true
}

func parseLanguageTag(value string) (language.Tag, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return language.Tag{}, false
	}
	if tag, err := language.Parse(trimmed); err == nil && tag != language.Und {
		return tag, true
	}
	normalized := languageutil.NormalizeLanguageDisplay(trimmed)
	if normalized == "" {
		normalized = trimmed
	}
	languageTagLookupOnce.Do(buildLanguageTagLookup)
	tag, ok := languageTagLookup[strings.ToLower(strings.TrimSpace(normalized))]
	if ok {
		return tag, true
	}
	return language.Tag{}, false
}

func buildLanguageTagLookup() {
	languageTagLookup = make(map[string]language.Tag)
	namer := display.Languages(language.English)
	for _, tag := range display.Supported.Tags() {
		name := strings.ToLower(strings.TrimSpace(namer.Name(tag)))
		if name == "" {
			continue
		}
		if _, exists := languageTagLookup[name]; exists {
			continue
		}
		languageTagLookup[name] = tag
	}
}

func resolveOriginalLanguage(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage)
	case meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.OriginalLanguage)
	default:
		return ""
	}
}

func isEnglishLanguageToken(value string) bool {
	normalized := languageutil.NormalizeLanguageDisplay(value)
	if normalized != "" {
		value = normalized
	}
	return isEnglishLanguageTag(strings.TrimSpace(value))
}

func isEnglishLanguageTag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "english", "en", "eng", "en-us", "en-gb":
		return true
	default:
		return false
	}
}

func isNoGroupTag(tag string) bool {
	value := strings.ToLower(strings.TrimSpace(tag))
	switch value {
	case "nogrp", "nogroup", "unknown", "-unk-":
		return true
	default:
		return false
	}
}
