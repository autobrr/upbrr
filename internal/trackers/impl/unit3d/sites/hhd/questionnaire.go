// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	subject := api.NewTrackerValidationSubject(input.Meta, "HHD")
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleCompatibility {
			continue
		}
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		if len(candidates) <= 1 {
			continue
		}
		options := []string{"unresolved"}
		details := ""
		for _, candidate := range candidates {
			options = append(options, candidate.ID)
			if details != "" {
				details += "; "
			}
			details += candidate.ID + " (" + candidate.Codec + ", " + string(candidate.Role) + ", " + candidate.Title + ")"
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID),
			Label:    "HHD source mix for compatibility track " + track.ID,
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  options,
			Help:     "Use source evidence to select this track's corresponding mix: " + details + ". Matching languages or codec counts alone do not establish association. At most one compatibility track is allowed per mix; every TrueHD mix still needs standalone AC-3.",
		})
	}
	for i := range fields {
		if value := subject.QuestionnaireAnswers[fields[i].Key]; slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "HHD", Fields: fields}
}
