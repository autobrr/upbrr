// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{RequireUniqueID: true, Language: englishNonDisc()}
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
