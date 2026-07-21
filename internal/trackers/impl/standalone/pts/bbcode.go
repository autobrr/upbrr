// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

var sceneNFOPattern = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? nfo:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	value = strings.NewReplacer(
		"[user]",
		"",
		"[/user]",
		"",
		"[align=left]",
		"",
		"[/align]",
		"",
		"[right]",
		"",
		"[/right]",
		"",
		"[align=right]",
		"",
		"[/align=right]",
		"",
		"[sup]",
		"",
		"[/sup]",
		"",
		"[sub]",
		"",
		"[/sub]",
		"",
		"[alert]",
		"",
		"[/alert]",
		"",
		"[note]",
		"",
		"[/note]",
		"",
		"[hr]",
		"",
		"[/hr]",
		"",
		"[h1]",
		"[u][b]",
		"[/h1]",
		"[/b][/u]",
		"[h2]",
		"[u][b]",
		"[/h2]",
		"[/b][/u]",
		"[h3]",
		"[u][b]",
		"[/h3]",
		"[/b][/u]",
		"[ul]",
		"",
		"[/ul]",
		"",
		"[ol]",
		"",
		"[/ol]",
		"",
		"[hide]",
		"",
		"[/hide]",
		"",
	).Replace(value)
	value = sceneNFOPattern.ReplaceAllString(value, "")
	value = bbcode.ConvertComparisonToCentered(value, 1000)
	value = bbcode.RemoveSpoiler(value)
	return bbcode.RemoveExtraLines(value)
}
