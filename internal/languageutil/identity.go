// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import "slices"

// IsIdentifiedLanguage accepts a named language or explicit non-linguistic
// content. A collective, und, mul or mis does not identify its member languages.
func IsIdentifiedLanguage(value string) bool {
	code := NormalizeLanguageCode(value)
	return code != "" && code != "und" && code != "mul" && code != "mis" && registryLanguages[code].scope != "collection"
}

// SameLanguage compares base identities while retaining variants in stored
// values. Identical unresolved text may match, but is not identified evidence.
func SameLanguage(left, right string) bool {
	left, _ = SubtitleLanguageParts(left)
	right, _ = SubtitleLanguageParts(right)
	leftCode, rightCode := NormalizeLanguageCode(left), NormalizeLanguageCode(right)
	if leftCode != "" || rightCode != "" {
		return leftCode != "" && leftCode == rightCode
	}
	return left != "" && languageNameKey(left) == languageNameKey(right)
}

// ContainsLanguage matches a language identity independently of label and coverage.
func ContainsLanguage(values []string, value string) bool {
	return slices.ContainsFunc(values, func(candidate string) bool { return SameLanguage(candidate, value) })
}

// LanguageIdentityCount counts distinct identified base values, including ZXX.
// Variants of one language and unresolved/collective values add no extra identity.
func LanguageIdentityCount(values []string) int {
	seen := make(map[string]bool)
	for _, value := range values {
		if IsIdentifiedLanguage(value) {
			seen[NormalizeLanguageCode(value)] = true
		}
	}
	return len(seen)
}

// LanguageMatchKeys returns distinct base-match keys in order. Unresolved values
// retain their normalized spelling; a key alone does not establish known evidence.
// This projection counts one language once within a stream without altering facts.
func LanguageMatchKeys(values []string) []string {
	keys := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		base, _ := SubtitleLanguageParts(value)
		key := NormalizeLanguageCode(base)
		if key == "" {
			key = languageNameKey(base)
		}
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}
