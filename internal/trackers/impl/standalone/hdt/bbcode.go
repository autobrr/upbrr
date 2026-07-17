// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[align=right]", "", "[/align]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[hr]", "", "[/hr]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
		"[ul]", "", "[/ul]", "",
		"[ol]", "", "[/ol]", "",
	)
	value = replacer.Replace(value)
	value = bbcode.RemoveSub(value)
	value = bbcode.RemoveSup(value)
	value = bbcode.ConvertSpoilerToHide(value)
	value = bbcode.RemoveImageResize(value)
	value = bbcode.ConvertComparisonToCentered(value, 1000)
	value = bbcode.RemoveSpoiler(value)
	value = bbcode.RemoveList(value)
	return bbcode.RemoveExtraLines(value)
}
