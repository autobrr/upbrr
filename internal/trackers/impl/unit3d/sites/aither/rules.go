// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"github.com/autobrr/upbrr/internal/trackers"
)

// Rules returns AITHER's strict unique-ID requirement and strict non-disc
// English audio-or-subtitle requirement, accepting original-language audio
// when English subtitles are present.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{RequireUniqueID: true, Language: englishNonDisc()}
}

// AudioPolicy allows English as an additional audio language.
func AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowedLanguages: []string{"english"}}
}
func englishNonDisc() *trackers.LanguageRule {
	return &trackers.LanguageRule{
		Languages:      []string{"english", "en", "eng"},
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonDisc: true,
	}
}
