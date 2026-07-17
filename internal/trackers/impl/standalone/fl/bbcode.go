// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	value = bbcode.RemoveSpoiler(value)
	value = bbcode.ConvertCodeToQuote(value)
	value = bbcode.ConvertComparisonToCentered(value, 900)
	value = bbcode.RemoveImageResize(value)
	return bbcode.RemoveExtraLines(value)
}
