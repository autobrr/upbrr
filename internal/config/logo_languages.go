// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

var logoLanguageTag = regexp.MustCompile(`^[a-z]{2}([_-]([a-z]{2}|[0-9]{3}))?$`)

var logoLanguageNames = func() map[string]string {
	names := make(map[string]string)
	for _, base := range language.Supported.BaseLanguages() {
		code := base.String()
		if name := display.English.Languages().Name(base); len(code) == 2 && name != "" {
			names[code] = name
		}
	}
	names["pt"] = "Portuguese (including Brazilian Portuguese / Brazil)"
	return names
}()

// LogoLanguageNames returns the base-code catalog for TMDB artwork, not regional metadata requests.
func LogoLanguageNames() map[string]string {
	return maps.Clone(logoLanguageNames)
}

// ParseLogoLanguages accepts legacy ordered comma/semicolon lists and reports unsupported entries separately.
func ParseLogoLanguages(value string) (codes, unsupported []string) {
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		tag := strings.ToLower(item)
		if !logoLanguageTag.MatchString(tag) || logoLanguageNames[tag[:2]] == "" {
			unsupported = append(unsupported, item)
			continue
		}
		if code := tag[:2]; !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	return codes, unsupported
}
