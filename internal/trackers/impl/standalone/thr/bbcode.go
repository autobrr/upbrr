// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

var sceneNFOPattern = regexp.MustCompile(`(?is)(\[hide=(?:Scene|FraMeSToR) NFO:\]\[pre\])(.*?)(\[/pre\]\[/hide\])`)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	value = bbcode.ConvertNamedSpoilerToNamedHide(value)
	value = bbcode.ConvertSpoilerToHide(value)
	value = bbcode.ConvertCodeToPre(value)
	value = sceneNFOPattern.ReplaceAllString(value, `$1[align=left]$2[/align]$3`)
	return bbcode.RemoveExtraLines(value)
}
