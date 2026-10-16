// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"regexp"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

var (
	sourceTVPathPattern = regexp.MustCompile(
		`(?i)(?:^|[\\/])(?:tv|tvshows?|tv.shows?|series|shows)(?:[\\/]|$)|(?:^|[\\/])season\s*\d+[\\/]|(?:^|[\\/])(?:S(?:\d{2}|\d{4})(?:E\d{1,3})?|season\s*(?:\d{2}|\d{4}))\b|\b(?:tv pack|season\s*(?:\d{2}|\d{4}))\b`,
	)
	sourceTVNamePattern = regexp.MustCompile(
		`(?i)\bS(?:\d{2}|\d{4})(?:E\d{1,3})?\b|\b(?:season|series)\s*(?:\d{2}|\d{4})\b|\bE\d{2,3}\s*-|\b\d{4}[.-]\d{1,2}[.-]\d{1,2}\b`,
	)
	sourceSubsPleasePattern   = regexp.MustCompile(`(?i)subsplease`)
	sourceAnimeEpisodePattern = regexp.MustCompile(`(?i)(?:\s-\s)?\d{1,3}\s*\((?:\d+p|480i|576i|1080i)\)`)
)

// sourceHasTVCategory recognizes source folder and basename hints without
// passing parent-directory text into the release-title parser. Spelled-out season
// directories supply category evidence without setting canonical season fields.
func sourceHasTVCategory(source string) bool {
	path := pathutil.Clean(source)
	return sourceTVPathPattern.MatchString(path) || sourceTVNamePattern.MatchString(pathutil.Base(path)) ||
		sourceSubsPleasePattern.MatchString(path) && sourceAnimeEpisodePattern.MatchString(path)
}

// detectedSourceCategory prefers TV source hints, then a supported parser category.
// Unknown remains empty so a final naming fallback cannot override identity evidence.
// Explicit and tracker categories are resolved later.
func detectedSourceCategory(source string, parsedCategory string) string {
	if sourceHasTVCategory(source) {
		return "TV"
	}
	return normalizeCategory(parsedCategory)
}
