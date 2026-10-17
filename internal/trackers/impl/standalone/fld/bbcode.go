// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
)

func finalizeDescription(value string) string {
	value = strings.ReplaceAll(value, "[user]", "")
	value = strings.ReplaceAll(value, "[/user]", "")
	value = strings.ReplaceAll(value, "[img]", "[img width=300]")
	return bbcode.RemoveExtraLines(value)
}
