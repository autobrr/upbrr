// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

func Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{RequireValidMISetting: true, Language: &ruletypes.LanguageRule{
		Languages:      []string{"english", "en", "eng"},
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonDisc: true,
	}}
}
