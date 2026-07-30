// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

var (
	urlSizedImagePattern = regexp.MustCompile(`(?i)\[url=(?P<href>[^\]]+)\]\[img=(?P<width>\d+)\](?P<src>[^\[]+)\[/img\]\[/url\]`)
	urlImagePattern      = regexp.MustCompile(`(?i)\[url=(?P<href>[^\]]+)\]\[img\](?P<src>[^\[]+)\[/img\]\[/url\]`)
	sizedImagePattern    = regexp.MustCompile(`(?i)\[img=(?P<width>\d+)\](?P<src>[^\[]+)\[/img\]`)
)

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
		"•",
		"-",
		"“",
		`"`,
		"”",
		`"`,
	).Replace(value)
	value = bbcode.RemoveSub(value)
	value = bbcode.RemoveSup(value)
	value = bbcode.ConvertComparisonToCentered(value, 1000)
	value = bbcode.RemoveSpoiler(value)
	value = urlSizedImagePattern.ReplaceAllString(value, `<a href="$href" target="_blank"><img src="$src" width="$width"></a>`)
	value = urlImagePattern.ReplaceAllString(value, `<a href="$href" target="_blank"><img src="$src" width="220"></a>`)
	value = sizedImagePattern.ReplaceAllString(value, `<img src="$src" width="$width">`)
	return bbcode.RemoveExtraLines(value)
}
