// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package ulcx

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules retains ULCX's independent media and release-type requirements.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{RequireValidMISetting: true, BlockDVDRip: true}
}
func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	policy := trackers.LanguagePolicy{
		ExtraDubs:               trackers.LanguageProhibited,
		EnglishSubtitles:        "foreign_without_dub",
		MissingSubtitles:        trackers.LanguageProhibited,
		CompatibilityRequired:   true,
		CompatibilityOnlyTrueHD: true,
		SubtitleDefault:         trackers.LanguageAdvisory,
	}
	if subject.PersonalRelease {
		policy.OriginalAudio = trackers.LanguageProhibited
		policy.SubtitleDefault = trackers.LanguageProhibited
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	if !subject.PersonalRelease && subject.LanguageFacts.OriginalLanguagesKnown &&
		subject.LanguageFacts.ProgrammeStatus == api.MetadataEvidenceStatusComplete &&
		subject.LanguageFacts.HasEnglishDub() && !subject.LanguageFacts.HasOriginalAudio() {
		failures = append(failures, trackers.LanguageRuleFailure(subject, "original_recommendation",
			"original audio should accompany the English dub", trackers.LanguageAdvisory))
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role == api.AudioRoleAlternateMix {
			answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ID)]
			if answer != "unique" {
				reason := "source review should establish meaningful uniqueness of alternate mix " + track.ID + "; a track title alone does not establish it"
				if answer == "duplicate" {
					reason = "alternate mix " + track.ID + " duplicates an existing mix; meaningfully unique mixes are welcomed"
				}
				failure := trackers.LanguageRuleFailure(subject, "alternate_mix", reason, trackers.LanguageAdvisory)
				if answer != "duplicate" {
					failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
				}
				failures = append(failures, failure)
			}
		}
		if track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleCommentary || track.Role == api.AudioRoleIsolatedScore) {
			outcome := trackers.LanguageUnresolved
			switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "language_secondary_retail")] {
			case "yes":
				continue
			case "no":
				outcome = trackers.LanguageProhibited
			}
			failures = append(
				failures,
				trackers.LanguageRuleFailure(subject, "secondary_source", "retail provenance of secondary track "+track.ID+" is required", outcome),
			)
		}
	}
	facts := subject.LanguageFacts
	if facts.OriginalLanguagesKnown {
		foreign := !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.OriginalLanguages, "ZXX")
		for _, track := range facts.Tracks {
			if track.Kind != api.MediaTrackSubtitle {
				continue
			}
			if !foreign && track.Default {
				failures = append(
					failures,
					trackers.LanguageRuleFailure(subject, "subtitle_default", "subtitles should not be default on non-foreign content", policy.SubtitleDefault),
				)
			}
			for _, value := range track.Languages {
				language, coverage := languageutil.SubtitleLanguageParts(value)
				if !track.Forced && !strings.EqualFold(coverage, "Forced") {
					continue
				}
				if !slices.Contains(facts.OriginalLanguages, language) && (language != "English" || !facts.HasEnglishDub()) {
					failures = append(
						failures,
						trackers.LanguageRuleFailure(
							subject,
							"forced_subtitle_language",
							"forced subtitle track "+track.ID+" uses a language outside the original and applicable English dub",
							trackers.LanguageProhibited,
						),
					)
				}
			}
		}
	}

	return failures
}
