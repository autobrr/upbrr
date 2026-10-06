// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

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
	subject := api.NewTrackerValidationSubject(meta, "BHD")
	fields := []api.TrackerQuestionnaireField{
		{
			Key:      trackers.LanguageQuestionKey(subject, "existing_release"),
			Label:    "BHD existing-release changes",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  []string{"unchanged_or_new", "tracks_or_chapters_added", "unresolved"},
			Help:     "Was audio, subtitles or chapters added to an existing release? Select unchanged_or_new only for an unchanged existing release or a newly produced release. Known additions require staff approval and remain blocked; this source-history answer does not establish staff permission.",
		},
	}
	if needsProgrammeMixReview(meta.LanguageFacts) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "programme_mixes"),
			Label:    "BHD same-language programme mixes",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  []string{"distinct_mixes", "duplicate_main_mix", "unresolved"},
			Help:     "Compare the same-language programme tracks against their source mixes. Are these genuinely distinct mixes or duplicate versions of one main mix in another format? An Alternate Mix title alone is insufficient. TrueHD Dolby Digital compatibility audio is assessed separately. This does not waive a prohibited dub or staff-only modification.",
		})
	}
	if strings.EqualFold(meta.Type, "REMUX") {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:     trackers.LanguageQuestionKey(subject, "source_extras"),
			Label:   "BHD remux source extras",
			Kind:    "select",
			Options: []string{"retained_or_unavailable", "incomplete", "unresolved"},
			Help:    "Available source subtitles, commentary and chapters should accompany remuxes. Select retained_or_unavailable only after checking that all available material is included or unavailable; incomplete records a known omission. This recommendation is advisory.",
		})
		if needsForeignDialogueReview(meta.LanguageFacts) {
			fields = append(fields, api.TrackerQuestionnaireField{
				Key:     trackers.LanguageQuestionKey(subject, "foreign_dialogue_subtitles"),
				Label:   "BHD remux foreign-dialogue subtitles",
				Kind:    "select",
				Options: []string{"not_required", "included", "missing", "unresolved"},
				Help:    "English subtitles for foreign dialogue should be separate and forced. Select included only when the required coverage is included as a separate forced English subtitle track, not merely full or hardcoded subtitles. This recommendation is advisory.",
			})
		}
	}
	for i := range fields {
		value := subject.QuestionnaireAnswers[fields[i].Key]
		if slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
	}
	return &api.TrackerQuestionnaire{Tracker: "BHD", Fields: fields}
}
