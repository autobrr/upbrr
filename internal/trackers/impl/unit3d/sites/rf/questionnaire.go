// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
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
	if !rfNeedsPredominanceEvidence(subject) {
		return nil
	}
	key := trackers.LanguageQuestionKey(subject, "predominantly_english")
	return &api.TrackerQuestionnaire{Tracker: "RF", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "Predominantly English film",
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Value:    subject.QuestionnaireAnswers[key],
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Is this film predominantly English, with the forced English subtitles covering foreign dialogue? Mixed original-language metadata alone does not establish that exception.",
		},
	}}
}
