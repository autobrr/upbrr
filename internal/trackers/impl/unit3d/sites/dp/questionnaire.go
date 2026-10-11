// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const manualLanguageMarkerKey = "manual_language_marker"
const omittedLanguageMarker = "(omit)"

// manualLanguageQuestionnaire requests an exact marker only when complete facts
// fall outside DP's established rows. The answer is bound to the current evidence.
func manualLanguageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	if trackers.IsFullDiscUpload(input.Meta.DiscType, input.Meta.Type) {
		return nil
	}
	_, established, err := audioLabelForFacts(input.Meta)
	if established || err != nil {
		return nil
	}
	subject := api.NewTrackerValidationSubject(input.Meta, "DP")
	key := trackers.LanguageQuestionKey(subject, manualLanguageMarkerKey)
	value := ""
	if _, ok := manualAudioLabel(subject); ok {
		value = strings.TrimSpace(subject.QuestionnaireAnswers[key])
	}
	return &api.TrackerQuestionnaire{Tracker: "DP", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "DP language marker",
			Kind:     "select",
			Options:  manualAudioLabelOptions(subject.LanguageFacts),
			Value:    value,
			Required: true,
			Help: "DP's naming guide does not establish a marker for this audio composition. Choose a marker explicitly, or (omit) to leave it out. A full-name override alone is not this choice. Clear any conflicting forced language marker first. This naming choice does not waive audio eligibility rules. Original: " + strings.Join(
				subject.LanguageFacts.OriginalLanguages,
				", ",
			) + "; programme audio: " + strings.Join(
				subject.LanguageFacts.ProgrammeLanguages,
				", ",
			) + ".",
		},
	}}
}

// manualAudioLabel accepts only an explicit current answer; empty input cannot
// silently select omission, and naming choices never change the language facts.
func manualAudioLabel(subject api.TrackerValidationSubject) (string, bool) {
	answer := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, manualLanguageMarkerKey)]
	if strings.ContainsAny(answer, "\r\n") {
		return "", false
	}
	answer = strings.TrimSpace(answer)
	if !slices.Contains(manualAudioLabelOptions(subject.LanguageFacts), answer) {
		return "", false
	}
	if answer == omittedLanguageMarker {
		return "", true
	}
	return answer, true
}

func manualAudioLabelOptions(facts api.LanguageFacts) []string {
	options := make([]string, 0, 4+2*len(facts.ProgrammeLanguages))
	options = append(options, omittedLanguageMarker, "Dubbed", "Dual-Audio", "MULTi")
	for _, language := range facts.ProgrammeLanguages {
		options = append(options, language+" Dubbed", language+" MULTi")
	}
	return options
}
