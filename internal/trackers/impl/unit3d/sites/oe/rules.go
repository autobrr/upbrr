// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import "github.com/autobrr/upbrr/internal/trackers"

// Rules returns OE's waivable adult-content restriction and strict non-disc
// English-language restriction.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockAdult:   true,
		AdultMessage: "Porn is not allowed",
		Language: &trackers.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			ApplyIfNonDisc: true,
		},
	}
}
