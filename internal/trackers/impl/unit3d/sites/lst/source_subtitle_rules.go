// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func remuxSubtitleFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if !strings.EqualFold(subject.Type, "REMUX") {
		return nil
	}
	failure := trackers.LanguageRuleFailure(
		subject,
		"retail_subtitles",
		"Retain applicable retail English subtitles and retail forced-English subtitles for non-English dialogue or on-screen text in English-language content. Compare every prepared resource with its retail source; preserved retail hardcoded forced subtitles can satisfy forced coverage. Missing retail tracks may make the release trumpable.",
		trackers.LanguageAdvisory,
	)
	failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
	return []api.RuleFailure{failure}
}

func alternateMixQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	var fields []api.TrackerQuestionnaireField
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleAlternateMix {
			continue
		}
		key := trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ResourceID+"_"+track.ID)
		value := subject.QuestionnaireAnswers[key]
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:         key,
				Label:       "Alternate mix source for resource " + track.ResourceID + ", track " + track.ID,
				Kind:        "select",
				Options:     []string{"unresolved", "additional_original_or_unique_remix", "not_unique"},
				Required:    subject.PersonalRelease,
				Value:       value,
				NeedsReview: value == "" || value == "unresolved",
				Help:        "Review the exact source mix and its distinguishing content; track labels alone do not establish uniqueness.",
			},
		)
	}
	return fields
}

func alternateMixFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	var failures []api.RuleFailure
	for _, field := range alternateMixQuestions(subject) {
		if field.Value == "additional_original_or_unique_remix" {
			continue
		}
		outcome := trackers.LanguageAdvisory
		if subject.PersonalRelease {
			outcome = trackers.LanguageUnresolved
			if field.Value == "not_unique" {
				outcome = trackers.LanguageProhibited
			}
		}
		failure := trackers.LanguageRuleFailure(subject, "alternate_mix", field.Label+": original-mix or unique-remix evidence is needed", outcome)
		failure.QuestionnaireInput = true
		if field.Value != "not_unique" {
			failure.EvidenceStatus = api.MetadataEvidenceStatusPartial
		}
		failures = append(failures, failure)
	}
	return failures
}
