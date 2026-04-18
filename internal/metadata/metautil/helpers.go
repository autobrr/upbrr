// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moistari/rls"

	"github.com/autobrr/upbrr/internal/pathutil"
)

type ParsedRelease struct {
	Title    string
	Alt      string
	Subtitle string
	Category string
	Year     int
}

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
		Category: releaseCategory(release.Type.String()),
		Year:     release.Year,
	}
}

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

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func FirstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ReduceTitle(filename string, drop int) string {
	words := strings.Fields(filename)
	if len(words) <= drop {
		return ""
	}
	extensions := map[string]struct{}{"mp4": {}, "mkv": {}, "avi": {}, "webm": {}, "mov": {}, "wmv": {}}
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

func releaseCategory(value string) string {
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
