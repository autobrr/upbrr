// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/trackers"
)

var antEmptyURLPattern = regexp.MustCompile(`(?is)\[url=[^\]]*]\s*\[/url\]`)
var antEmptyAlignmentPattern = regexp.MustCompile(`(?is)\[(?:center|left|right|align=(?:center|left|right))\]\s*\[/(?:center|left|right|align)\]`)
var antSizePattern = regexp.MustCompile(`(?i)\[/?size(?:=[^\]]*)?\]`)

func finalizeDescription(value string) string {
	return description.MapOutsideLiteralBlocks(value, func(value string) string {
		value = trackers.StripDefaultDescriptionSignature(value)
		value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
		value = bbcode.ConvertToAlign(value)
		value = bbcode.RemoveImageResize(value)
		value = bbcode.RemoveSup(value)
		value = bbcode.RemoveSub(value)
		value = bbcode.ConvertSpoilerToHide(value)
		value = antSizePattern.ReplaceAllString(value, "")
		value = strings.NewReplacer(
			"[h1]", "[b]", "[/h1]", "[/b]",
			"[h2]", "[b]", "[/h2]", "[/b]",
			"[h3]", "[b]", "[/h3]", "[/b]",
			"[ul]", "", "[/ul]", "", "[ol]", "", "[/ol]", "",
			"[li]", "- ", "[/li]", "\n", "[hr]", "---",
		).Replace(value)
		value = bbcode.RemoveList(value)
		return bbcode.RemoveExtraLines(value)
	})
}
