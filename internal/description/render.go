// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"regexp"
	"strings"
)

var htmlTagPattern = regexp.MustCompile(`(?i)<[a-z][^>]*>`)

// Render converts BBCode, MediaInfo blocks, or existing HTML into HTML that has
// passed the package's element, attribute, class, style, and URL allowlists.
// Blank input returns an empty string.
func Render(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if rendered, ok := renderBBCodeWithMediaInfo(trimmed); ok {
		return sanitizeHTML(rendered)
	}
	if looksLikeHTML(trimmed) {
		return sanitizeHTML(trimmed)
	}
	return sanitizeHTML(renderBBCode(trimmed))
}

func looksLikeHTML(value string) bool {
	return htmlTagPattern.MatchString(value)
}
