// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import "github.com/autobrr/upbrr/internal/trackers/ruletypes"

// Rules declares NBL's TV-only and English-language requirements.
func (d *Definition) Rules() *ruletypes.RuleSet {
	return &ruletypes.RuleSet{
		RequireTVOnly: true,
		Language: &ruletypes.LanguageRule{
			Languages:      []string{"english", "en", "eng"},
			RequireAudio:   true,
			RequireSubs:    true,
			AllowOriginal:  true,
			ApplyIfNonBDMV: true,
		},
	}
}
