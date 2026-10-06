// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// ProjectionQuestionnaire collects only the established AZ dialect exception.
// The evidence-bound key prevents an answer applying to changed audio facts.
func (d *Definition) ProjectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	if d.site.Name != "AZ" || trackers.IsFullDiscUpload(input.Meta.DiscType, input.Meta.Type) {
		return nil
	}
	subject := api.NewTrackerValidationSubject(input.Meta, "AZ")
	dialects := azDialectLanguages(subject)
	if len(dialects) == 0 {
		return nil
	}
	key := trackers.LanguageQuestionKey(subject, "regional_dialect")
	value := subject.QuestionnaireAnswers[key]
	if value != "yes" && value != "no" {
		value = ""
	}
	return &api.TrackerQuestionnaire{Tracker: "AZ", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "Regional dialects: " + strings.Join(dialects, ", "),
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Value:    value,
			Required: true,
			Help: "Are all listed programme languages regional dialects of the original " + strings.Join(
				subject.LanguageFacts.OriginalLanguages,
				", ",
			) + " audio? This attests the language relationship only; voice-over and independent track rules remain applicable.",
		},
	}}
}

func azDialectLanguages(subject api.TrackerValidationSubject) []string {
	facts := subject.LanguageFacts
	if !facts.OriginalLanguagesKnown || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete ||
		(strings.EqualFold(subject.Type, "REMUX") && (facts.HasOriginalAudio() || slices.Contains(facts.ProgrammeLanguages, "English"))) {
		return nil
	}
	var dialects []string
	for _, language := range facts.ProgrammeLanguages {
		if language != "English" && !slices.Contains(facts.OriginalLanguages, language) {
			dialects = append(dialects, language)
		}
	}
	return dialects
}
