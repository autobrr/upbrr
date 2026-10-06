// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import "github.com/autobrr/upbrr/internal/trackers"

// rules declares NBL's TV-only and English-language requirements.
func rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireTVOnly: true,
	}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{EnglishSubtitles: "without_english", MissingSubtitles: trackers.LanguageProhibited}
}
