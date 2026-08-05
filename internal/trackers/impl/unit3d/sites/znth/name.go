// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package znth

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// buildZNTHName applies ZNTH release-name policy before upload.
// TV names drop episode-title text when it appears before the resolution.
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	category := unit3d.Category(meta)
	if category == "TV" && strings.TrimSpace(meta.EpisodeTitle) != "" {
		resolution := unit3d.Resolution(meta)
		if resolution != "" {
			name = replaceZNTHEpisodeTitle(name, meta.EpisodeTitle, resolution)
		}
	}

	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

// replaceZNTHEpisodeTitle removes the episode-title segment only when its
// normalized text appears immediately before a matching resolution token.
func replaceZNTHEpisodeTitle(name string, episodeTitle string, resolution string) string {
	normalizedTitle := normalizeZNTHAlphaNum(episodeTitle)
	if normalizedTitle == "" {
		return name
	}

	for _, resolutionStart := range findZNTHTokenIndexes(name, resolution) {
		titleStart, ok := findZNTHTitleStartBefore(name[:resolutionStart], normalizedTitle)
		if !ok {
			continue
		}
		return name[:titleStart] + name[resolutionStart:]
	}
	return name
}

// findZNTHTitleStartBefore returns the byte offset of the trailing segment in
// prefix whose alphanumeric-normalized text matches normalizedTitle.
func findZNTHTitleStartBefore(prefix string, normalizedTitle string) (int, bool) {
	candidates := []int{0}
	for i, r := range prefix {
		if !isZNTHAlphaNum(r) {
			candidates = append(candidates, i+len(string(r)))
		}
	}

	for i := len(candidates) - 1; i >= 0; i-- {
		start := candidates[i]
		if normalizeZNTHAlphaNum(prefix[start:]) == normalizedTitle {
			return start, true
		}
	}
	return 0, false
}

// findZNTHTokenIndexes returns original-string byte offsets for
// case-insensitive token matches bounded by non-alphanumeric ZNTH separators.
func findZNTHTokenIndexes(value string, token string) []int {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	tokenRunes := utf8.RuneCountInString(token)
	indexes := []int{}
	for start := range value {
		end, ok := endAfterZNTHRunes(value, start, tokenRunes)
		if !ok {
			break
		}
		if strings.EqualFold(value[start:end], token) && hasZNTHTokenBoundaries(value, start, end) {
			indexes = append(indexes, start)
		}
	}
	return indexes
}

// endAfterZNTHRunes returns the byte offset after count runes from start.
func endAfterZNTHRunes(value string, start int, count int) (int, bool) {
	end := start
	for range count {
		if end >= len(value) {
			return 0, false
		}
		_, size := utf8.DecodeRuneInString(value[end:])
		end += size
	}
	return end, true
}

// hasZNTHTokenBoundaries reports whether start and end are outside adjacent
// letters or digits in value.
func hasZNTHTokenBoundaries(value string, start int, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(value[:start])
		if isZNTHAlphaNum(r) {
			return false
		}
	}
	if end < len(value) {
		r, _ := utf8.DecodeRuneInString(value[end:])
		if isZNTHAlphaNum(r) {
			return false
		}
	}
	return true
}

// normalizeZNTHAlphaNum lowercases value and drops every non-alphanumeric rune.
func normalizeZNTHAlphaNum(value string) string {
	var b strings.Builder
	for _, r := range value {
		if isZNTHAlphaNum(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func isZNTHAlphaNum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
