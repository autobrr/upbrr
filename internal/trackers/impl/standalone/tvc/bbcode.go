// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

func finalizeDescription(value string) string {
	value = strings.TrimSpace(bbcode.NormalizeNewlines(value))
	value = bbcode.ConvertPreToCode(value)
	value = bbcode.ConvertHideToSpoiler(value)
	value = bbcode.ConvertComparisonToCollapse(value, 1000)
	return bbcode.RemoveExtraLines(value)
}
