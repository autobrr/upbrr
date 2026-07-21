// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import "github.com/autobrr/upbrr/internal/trackers"

// Rules strictly requires valid MediaInfo encode settings and applies a
// strict non-disc English audio-or-subtitle requirement.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{RequireValidMISetting: true, Language: &trackers.LanguageRule{
		Languages:      []string{"english", "en", "eng"},
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonDisc: true,
	}}
}
