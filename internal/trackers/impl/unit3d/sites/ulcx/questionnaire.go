// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	subject := api.NewTrackerValidationSubject(input.Meta, "ULCX")
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	if slices.ContainsFunc(subject.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleCommentary || track.Role == api.AudioRoleIsolatedScore)
	}) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "language_secondary_retail"),
			Label:    "Retail commentary and isolated-score source",
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Do all commentary and isolated-score tracks come from retail sources? This records source evidence, not permission to bypass a prohibited track.",
		})
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role == api.AudioRoleAlternateMix {
			fields = append(fields, api.TrackerQuestionnaireField{
				Key:     trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ID),
				Label:   "ULCX alternate-mix source evidence for " + track.ID,
				Kind:    "select",
				Options: []string{"unique", "duplicate", "unresolved"},
				Help:    "Does source review establish that this is a meaningfully unique alternate mix? A track title alone does not establish uniqueness. This is guidance and does not permit otherwise prohibited programme dubs.",
			})
		}
	}
	for i := range fields {
		if value := subject.QuestionnaireAnswers[fields[i].Key]; slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ULCX", Fields: fields}
}
