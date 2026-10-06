// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	facts := meta.LanguageFacts
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) || !facts.OriginalLanguagesKnown || facts.SubtitleStatus != api.MetadataEvidenceStatusComplete {
		return nil
	}
	subject := api.NewTrackerValidationSubject(meta, "RF")
	schema := &api.TrackerQuestionnaire{Tracker: "RF"}
	add := func(key, label, help string) {
		key = trackers.LanguageQuestionKey(subject, key)
		schema.Fields = append(schema.Fields, api.TrackerQuestionnaireField{
			Key:      key,
			Label:    label,
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Value:    meta.TrackerQuestionnaireAnswers["RF"][key],
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     help,
		})
	}
	if !slices.Contains(facts.OriginalLanguages, "English") && !slices.Contains(facts.SubtitleLanguages, "English") {
		add(
			"english_subtitles_expected",
			"Retail source English subtitles",
			"Are English subtitles normally supplied or expected from this release's retail source? If yes, their absence is a Trumpable release defect requiring a separate acknowledgement.",
		)
	}
	if rfNeedsPredominanceEvidence(subject) {
		add(
			"predominantly_english",
			"Predominantly English film",
			"Is this film predominantly English, with the forced English subtitles covering foreign dialogue? Mixed original-language metadata alone does not establish that exception.",
		)
	}
	if len(schema.Fields) == 0 {
		return nil
	}
	return schema
}
