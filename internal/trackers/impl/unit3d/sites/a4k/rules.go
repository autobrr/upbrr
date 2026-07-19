// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package a4k

import "github.com/autobrr/upbrr/internal/trackers"

// Rules returns A4K's strict non-disc English audio-or-subtitle requirement,
// which accepts original-language audio when English subtitles are present.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{Language: &trackers.LanguageRule{
		Languages:      []string{"english", "en", "eng"},
		RequireAudio:   true,
		RequireSubs:    true,
		AllowOriginal:  true,
		ApplyIfNonDisc: true,
	}}
}
