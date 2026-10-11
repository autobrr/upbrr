// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"github.com/autobrr/upbrr/internal/trackers"
)

// Rules returns AITHER's strict unique-ID requirement. Its validation policy
// separately assesses non-disc language requirements and staff-only exceptions.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{RequireUniqueID: true}
}

// AudioPolicy allows English as an additional audio language.
func AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowedLanguages: []string{"english"}}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{
		OriginalAudio:    trackers.LanguageStaffException,
		ExtraDubs:        trackers.LanguageStaffException,
		EnglishSubtitles: "foreign_without_dub",
		MissingSubtitles: trackers.LanguageStaffException,
	}
}
