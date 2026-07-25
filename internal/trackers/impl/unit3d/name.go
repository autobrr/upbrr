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

func buildUnit3DName(_ string, meta api.UploadSubject, cfg config.TrackerConfig, profiles ...SiteProfile) string {
	profile := firstSiteProfile(profiles)
	if profile.BuildName != nil {
		return profile.BuildName(meta, cfg)
	}
	name := baseReleaseName(meta)
	if name == "" {
		return ""
	}

	return name
}

func baseReleaseName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func addNoGroupSuffix(name string, meta api.UploadSubject, suffix string) string {
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

func resolveOriginalLanguage(meta api.UploadSubject) string {
	switch {
	case meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.OriginalLanguage)
	case meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage) != "":
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.OriginalLanguage)
	default:
		return ""
	}
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
