// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package lume

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Rules retains LUME's independent media and adult-content requirements.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		RequireValidMISetting: true,
		BlockAdult:            true,
		AdultMessage:          "Porn is not allowed on LUME.",
	}
}
func personalRecommendationOutcome(subject api.TrackerValidationSubject) trackers.LanguageOutcome {
	if !subject.PersonalRelease {
		return trackers.LanguageAdvisory
	}
	switch subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "language_personal_exemption")] {
	case "previously_uploaded":
		return trackers.LanguageAdvisory
	case "trash_tier":
		if !subject.Anime {
			return trackers.LanguageAdvisory
		}
	case "anime_tier":
		if subject.Anime {
			return trackers.LanguageAdvisory
		}
	case "none":
		return trackers.LanguageProhibited
	}
	return trackers.LanguageUnresolved
}
func languageAssessment(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	facts := subject.LanguageFacts
	recommendation := personalRecommendationOutcome(subject)
	policy := trackers.LanguagePolicy{
		OriginalAudio:    trackers.LanguageProhibited,
		EnglishSubtitles: "spoken",
		MissingSubtitles: trackers.LanguageProhibited,
		OriginalFirst:    recommendation,
	}
	if facts.AudioAbsent {
		policy.OriginalAudio = ""
	}
	failures := trackers.EvaluateLanguagePolicy(subject, policy)
	if !slices.Contains(facts.OriginalLanguages, "ZXX") && slices.Contains(facts.SubtitleLanguages, "English") &&
		!slices.Contains(facts.FullSubtitleLanguages, "English") {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(
				subject,
				"subtitle_coverage",
				"complete English subtitles corresponding to original dialogue are not established",
				trackers.LanguageUnresolved,
			),
		)
	}
	var dubs []string
	for _, track := range facts.Tracks {
		if len(track.Languages) == 0 ||
			slices.ContainsFunc(track.Languages, func(value string) bool { return languageutil.NormalizeLanguageCode(value) == "" }) {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"track_language",
					"correct language assignment is unresolved for track "+track.ID,
					trackers.LanguageUnresolved,
				),
			)
		}
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			continue
		}
		for _, language := range track.Languages {
			if !slices.Contains(facts.OriginalLanguages, language) {
				dubs = append(dubs, language)
			}
		}
	}
	if len(dubs) > 1 {
		expected := slices.Clone(dubs)
		slices.SortFunc(expected, func(a, b string) int {
			if a == "English" && b != "English" {
				return -1
			}
			if b == "English" && a != "English" {
				return 1
			}
			return strings.Compare(a, b)
		})
		if !slices.Equal(dubs, expected) {
			failures = append(
				failures,
				trackers.LanguageRuleFailure(
					subject,
					"dub_order",
					"normal dubs should have English first and remaining languages alphabetically ordered",
					recommendation,
				),
			)
		}
	}
	return failures
}
