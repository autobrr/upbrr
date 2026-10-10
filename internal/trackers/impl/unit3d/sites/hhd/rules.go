// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules strictly blocks DVDRip uploads.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockDVDRip:           true,
		RequireValidMISetting: true,
	}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{
		OriginalAudio: trackers.LanguageProhibited,
		ExtraDubs:     trackers.LanguageProhibited,
	}
}

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures := trackers.EvaluateLanguagePolicy(subject, languagePolicy())
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return failures
	}
	facts := subject.LanguageFacts
	englishDubs := 0
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) && slices.Contains(track.Languages, "English") &&
			!slices.Contains(facts.OriginalLanguages, "English") {
			englishDubs++
		}
	}
	if englishDubs > 1 {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"english_dub_count",
				"at most one English programme dub may accompany a foreign title",
				trackers.LanguageProhibited,
			),
		)
	}
	failures = append(failures, sourceLanguageFailures(subject)...)
	return append(failures, compatibilityFailures(subject)...)
}
