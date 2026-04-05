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

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func releaseCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "movie"):
		return "MOVIE"
	case value != "":
		return "TV"
	default:
		return ""
	}
}
