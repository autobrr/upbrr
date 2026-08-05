// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

var (
	codeTagPattern  = regexp.MustCompile(`(?is)\[c\](.*?)\[/c\]`)
	imageTagPattern = regexp.MustCompile(`(?i)\[img=[\d"x]+\]`)
	listItemPattern = regexp.MustCompile(`(?i)\[\*\]`)
	hrPattern       = regexp.MustCompile(`(?i)\[hr\]`)
)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	value = strings.ReplaceAll(value, "[center]", "<center>")
	value = strings.ReplaceAll(value, "[/center]", "</center>")
	value = listItemPattern.ReplaceAllString(value, "\n[*]")
	value = codeTagPattern.ReplaceAllString(value, "[code]$1[/code]")
	value = hrPattern.ReplaceAllString(value, "---")
	value = imageTagPattern.ReplaceAllString(value, "[img]")
	value = strings.NewReplacer("[*] ", "• ", "[*]", "• ", "[note]", "Note: ", "[/note]", "", "[code]", "", "[/code]", "").Replace(value)
	value = bbcode.RemoveList(value)
	value = bbcode.ConvertComparisonToCentered(value, 1000)
	value = bbcode.RemoveSpoiler(value)
	return bbcode.RemoveExtraLines(value)
}
