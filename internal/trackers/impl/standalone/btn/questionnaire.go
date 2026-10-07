// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	subject := api.NewTrackerValidationSubject(meta, "BTN")
	language := btnPrimaryProgrammeLanguage(meta.LanguageFacts)
	if language == "" || isBTNEnglishLanguage(language) {
		return nil
	}
	key := trackers.LanguageQuestionKey(subject, "primary_audio_country")
	value := subject.QuestionnaireAnswers[key]
	if btnPrimaryCountryID(subject) == "" {
		value = ""
	}
	return &api.TrackerQuestionnaire{
		Tracker: "BTN",
		Fields: []api.TrackerQuestionnaireField{
			{
				Key:      key,
				Label:    "BTN country for primary " + language + " audio",
				Kind:     "text",
				Value:    value,
				Required: true,
				Help:     "Enter the BTN country matching the primary programme audio, using a supported country name or code. The work's origin country does not establish a dub's country. This does not authorize staff-only or prohibited content.",
			},
		},
	}
}
