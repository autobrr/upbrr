// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"html"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

var (
	azTagStripPattern  = regexp.MustCompile(`(?is)\[/?(?:size|align|left|center|right|img|table|tr|td|spoiler|url)[^\]]*\]`)
	azNFOStripPattern  = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? NFO:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)
	azLinkStripPattern = regexp.MustCompile(`https?://\S+|www\.\S+`)
)

func buildDescription(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	trimmed = azNFOStripPattern.ReplaceAllString(trimmed, "")
	trimmed = azLinkStripPattern.ReplaceAllString(trimmed, "")
	trimmed = azTagStripPattern.ReplaceAllString(trimmed, "")
	escaped := html.EscapeString(strings.TrimSpace(trimmed))
	lines := strings.Split(escaped, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
	}
	return strings.Join(cleaned, "<br>\n")
}

func buildDescriptionFromAssets(_ context.Context, req trackers.PreparationInput) string {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		return ""
	}
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	return buildDescription(assets.Description)
}
