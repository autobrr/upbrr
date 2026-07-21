// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import (
	"strings"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

var (
	languageNameMapOnce sync.Once
	languageNameMap     map[string]language.Tag
)

// NormalizeLanguageDisplay returns the English base-language label resolved from
// the first code or name token. Region and trailing tokens are discarded; blank
// or unresolved input returns an empty string.
func NormalizeLanguageDisplay(value string) string {
	token := normalizeLanguageToken(value)
	if token == "" {
		return ""
	}
	langTag, ok := resolveLanguageTag(token)
	if !ok {
		return ""
	}
	name := strings.TrimSpace(display.Languages(language.English).Name(langTag))
	if name == "" {
		return ""
	}
	return baseDisplayName(name)
}

func normalizeLanguageToken(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		switch r {
		case '-', '_', ',', ' ':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[0]))
}

func resolveLanguageTag(token string) (language.Tag, bool) {
	if token == "" {
		return language.Tag{}, false
	}
	if tag, err := language.Parse(token); err == nil && tag != language.Und {
		return tag, true
	}
	if tag := language.Make(token); tag != language.Und {
		return tag, true
	}
	return lookupLanguageTagByName(token)
}

func lookupLanguageTagByName(token string) (language.Tag, bool) {
	languageNameMapOnce.Do(buildLanguageNameMap)
	key := strings.ToLower(strings.TrimSpace(token))
	if key == "" {
		return language.Tag{}, false
	}
	tag, ok := languageNameMap[key]
	return tag, ok
}

func buildLanguageNameMap() {
	languageNameMap = make(map[string]language.Tag)
	namer := display.Languages(language.English)
	for _, tag := range display.Supported.Tags() {
		name := strings.TrimSpace(namer.Name(tag))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := languageNameMap[key]; exists {
			continue
		}
		languageNameMap[key] = tag
	}
}

func baseDisplayName(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	base := strings.ToLower(fields[0])
	if base == "" {
		return ""
	}
	if len(base) == 1 {
		return strings.ToUpper(base)
	}
	return strings.ToUpper(base[:1]) + base[1:]
}
