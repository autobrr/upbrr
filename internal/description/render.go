// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"regexp"
	"strings"
)

var (
	htmlTagPattern       = regexp.MustCompile(`(?i)<[a-z][^>]*>`)
	bbcodeOpeningPattern = regexp.MustCompile(
		`\[(b|i|u|s|url|img|spoiler|quote|list|left|right|center|align|comparison|code|hide|mediainfo|font|color)` +
			`(?:=[^\]]*|[ \t]+[^\]]*)?\]`,
	)
)

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
	// HTML-like text inside a BBCode block does not make the description HTML.
	if looksLikeHTML(trimmed) && !looksLikeBBCode(trimmed) {
		return sanitizeHTML(trimmed)
	}
	return sanitizeHTML(renderBBCode(trimmed))
}

func looksLikeHTML(value string) bool {
	return htmlTagPattern.MatchString(value)
}

func looksLikeBBCode(value string) bool {
	lower := strings.ToLower(value)
	for _, opening := range bbcodeOpeningPattern.FindAllStringSubmatch(lower, -1) {
		if strings.Contains(lower, "[/"+opening[1]+"]") {
			return true
		}
	}
	return false
}
