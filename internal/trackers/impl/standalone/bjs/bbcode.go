// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	value = bbcode.ConvertNamedSpoilerToNamedHide(value)
	value = bbcode.ConvertSpoilerToHide(value)
	value = bbcode.RemoveImageResize(value)
	value = bbcode.ConvertToAlign(value)
	value = bbcode.RemoveList(value)
	return bbcode.RemoveExtraLines(value)
}
