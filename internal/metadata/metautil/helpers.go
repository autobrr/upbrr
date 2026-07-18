// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moistari/rls"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

// ParsedRelease is the subset of basename parser output used by metadata
// provider searches.
type ParsedRelease struct {
	Title    string
	Alt      string
	Subtitle string
	Category string
	Year     int
}

// ParseRelease parses the host-path basename and normalizes its category to
// MOVIE, TV, or empty.
func ParseRelease(filename string) ParsedRelease {
	base := strings.TrimSpace(filename)
	if base == "" {
		return ParsedRelease{}
	}
	base = pathutil.Base(base)
	release := rls.ParseString(base)
	return ParsedRelease{
		Title:    release.Title,
		Alt:      release.Alt,
		Subtitle: release.Subtitle,
		Category: ReleaseCategoryFromRLS(release.Type.String()),
		Year:     release.Year,
	}
}

// NormalizeIMDbID adds the tt prefix and seven-digit padding to numeric IDs.
// Empty and zero values become empty; non-numeric input is preserved verbatim
// after trimming.
func NormalizeIMDbID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "0" {
		return ""
	}
	if strings.HasPrefix(trimmed, "tt") {
		return trimmed
	}
	id, err := strconv.Atoi(trimmed)
	if err != nil {
		return trimmed
	}
	return fmt.Sprintf("tt%07d", id)
}

// ParseIMDbNumeric accepts a trimmed numeric or tt-prefixed ID and returns zero
// for malformed input.
func ParseIMDbNumeric(value string) int {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "tt")
	value = strings.Trim(value, "/")
	if value == "" {
		return 0
	}
	id, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return id
}

func FirstInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// FirstNonEmpty returns the first value containing non-whitespace text without
// trimming the returned value.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// FirstNonEmptyTrimmed returns the first non-empty value after trimming it.
func FirstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// ReduceTitle splits on whitespace, removes standalone common video-extension
// tokens, and drops the requested number of trailing tokens. It returns empty
// when too few tokens remain.
func ReduceTitle(filename string, drop int) string {
	words := strings.Fields(filename)
	if len(words) <= drop {
		return ""
	}
	extensions := map[string]struct{}{
		"mp4":  {},
		"mkv":  {},
		"avi":  {},
		"webm": {},
		"mov":  {},
		"wmv":  {},
	}
	filtered := make([]string, 0, len(words))
	for _, word := range words {
		if _, ok := extensions[strings.ToLower(word)]; ok {
			continue
		}
		filtered = append(filtered, word)
	}
	if len(filtered) <= drop {
		return ""
	}
	return strings.Join(filtered[:len(filtered)-drop], " ")
}

// SimilarityRatio returns a case-sensitive, rune-aware recursive
// longest-common-substring score in the range 0 to 1.
func SimilarityRatio(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	matches := float64(matchCount([]rune(a), []rune(b)))
	if matches == 0 {
		return 0
	}
	total := float64(len([]rune(a)) + len([]rune(b)))
	return (2 * matches) / total
}

func AbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// ReleaseCategoryFromRLS collapses supported parser categories to MOVIE or TV
// and returns empty for unknown categories.
func ReleaseCategoryFromRLS(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	switch upper {
	case "MOVIE":
		return "MOVIE"
	case "EP", "EPS", "EPISODE", "SEASON", "SEASONPACK", "SERIES", "TV", "TVSHOW", "TV-SHOW":
		return "TV"
	default:
		return ""
	}
}

func matchCount(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	la, lb, length := longestCommonSubstring(a, b)
	if length == 0 {
		return 0
	}
	return length + matchCount(a[:la], b[:lb]) + matchCount(a[la+length:], b[lb+length:])
}

func longestCommonSubstring(a, b []rune) (int, int, int) {
	longest := 0
	endA := 0
	endB := 0

	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
	}

	for i, aRune := range a {
		row := i + 1
		for j, bRune := range b {
			col := j + 1
			if aRune == bRune {
				matrix[row][col] = matrix[row-1][col-1] + 1
				if matrix[row][col] > longest {
					longest = matrix[row][col]
					endA = row
					endB = col
				}
			}
		}
	}

	return endA - longest, endB - longest, longest
}
