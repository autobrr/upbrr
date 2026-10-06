// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn/xxx is not allowed at BHD.",
	}
}

func languagePolicy() trackers.LanguagePolicy {
	return trackers.LanguagePolicy{
		OriginalPrimary:         true,
		OriginalAudio:           trackers.LanguageProhibited,
		ExtraDubs:               trackers.LanguageProhibited,
		CompatibilityOnlyTrueHD: true,
		CompatibilityCodecs:     []string{"DD", "AC-3"},
	}
}

func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	failures := trackers.EvaluateLanguagePolicy(subject, languagePolicy())
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return failures
	}
	facts := subject.LanguageFacts
	originalProgramme := 0
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role == api.AudioRoleProgramme &&
			slices.ContainsFunc(track.Languages, func(language string) bool { return slices.Contains(facts.OriginalLanguages, language) }) {
			originalProgramme++
		}
	}
	if originalProgramme > 1 {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"redundant_original",
				"multiple original programme tracks need mix evidence; duplicate versions of the main mix in another format are prohibited except TrueHD Dolby Digital compatibility audio",
				trackers.LanguageUnresolved,
			),
		)
	}
	return failures
}
