// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

func finalizeDescription(value string) string {
	return bbcode.RemoveExtraLines(strings.TrimSpace(bbcode.NormalizeNewlines(value)))
}
