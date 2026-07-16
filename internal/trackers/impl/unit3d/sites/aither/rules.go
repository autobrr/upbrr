// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/ruletypes"
)

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{RequireUniqueID: true, Language: englishNonDisc()}
}

// AudioPolicy allows English as an additional audio language.
func AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowedLanguages: []string{"english"}}
}
func englishNonDisc() *ruletypes.LanguageRule {
	return &ruletypes.LanguageRule{
		Languages:      []string{"english", "en", "eng"},
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonDisc: true,
	}
}
