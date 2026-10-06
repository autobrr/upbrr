// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"slices"
	"strings"

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
		OriginalPrimary:       true,
		OriginalAudio:         trackers.LanguageProhibited,
		ExtraDubs:             trackers.LanguageProhibited,
		EnglishSubtitles:      "foreign",
		MissingSubtitles:      trackers.LanguageProhibited,
		CompatibilityRequired: true,
		CompatibilityCodecs:   []string{"DD", "AC-3"},
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
		if strings.Contains(strings.ToLower(track.Codec), "truehd") {
			matches := 0
			for _, compat := range facts.Tracks {
				if compat.Kind == api.MediaTrackAudio && compat.Role == api.AudioRoleCompatibility &&
					slices.ContainsFunc(compat.Languages, func(language string) bool { return slices.Contains(track.Languages, language) }) {
					matches++
				}
			}
			if matches > 1 {
				failures = append(
					failures,
					trackers.LanguageRuleFailure(
						subject,
						"compatibility_count",
						"at most one compatibility track is permitted per mix; the supplied same-language compatibility set needs mix association",
						trackers.LanguageUnresolved,
					),
				)
			}
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
	return failures
}
