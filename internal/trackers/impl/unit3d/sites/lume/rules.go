// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"github.com/autobrr/upbrr/internal/trackers"
)

// Rules strictly requires valid MediaInfo encode settings and enforces LUME's
// non-disc container, resolution, and language limits. Adult failures remain
// waivable; language failures are strict.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn is not allowed on LUME.",
		Language: &trackers.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonDisc: true,
		},
	}
}
