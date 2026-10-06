// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	subject := api.NewTrackerValidationSubject(meta, "HDB")
	var fields []api.TrackerQuestionnaireField
	if strings.EqualFold(meta.Type, "REMUX") {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "source_audio"),
			Label:    "HDB remux source audio",
			Kind:     "select",
			Required: true,
			Options:  []string{"best_original_retained", "source_mix_pair_retained", "incomplete", "unresolved"},
			Help:     "Confirm the best available original-language soundtrack is retained. Select source_mix_pair_retained when the source has both an original mono/stereo mix and surround upmix and both are retained; the original mix must be default. Select best_original_retained only when that source-pair requirement does not apply. Select incomplete for a known omission, or unresolved if the source cannot be established.",
		})
	}
	if needsForeignDialogueReview(meta.LanguageFacts) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "foreign_dialogue_subtitles"),
			Label:    "HDB foreign-dialogue subtitles",
			Kind:     "select",
			Required: true,
			Options:  []string{"not_required", "included", "missing", "unresolved"},
			Help:     "Does foreign dialogue require forced subtitles? Select not_required only when no such subtitle requirement applies, included when the required dialogue is covered by an included forced/default track, missing for a known omission, or unresolved when this cannot be established. These answers do not waive another language rule.",
		})
	}
	for i := range fields {
		value := subject.QuestionnaireAnswers[fields[i].Key]
		if slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "HDB", Fields: fields}
}
