// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pt

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	data["audio_pt"] = boolString(hasEuropeanPortuguese(req.Meta.AudioLanguages))
	data["legenda_pt"] = boolString(hasEuropeanPortuguese(req.Meta.SubtitleLanguages))
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func hasEuropeanPortuguese(languages []string) bool {
	for _, language := range languages {
		lower := strings.ToLower(strings.TrimSpace(language))
		if lower == "" || strings.Contains(lower, "brazil") || strings.Contains(lower, "brasil") || strings.Contains(lower, "pt-br") ||
			strings.Contains(lower, "ptbr") {
			continue
		}
		if strings.Contains(lower, "portuguese") || lower == "pt" || strings.Contains(lower, "português") {
			return true
		}
	}
	return false
}
