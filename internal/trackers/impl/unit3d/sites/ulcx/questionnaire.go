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
	_, assignmentReview := compatibilityAssessment(subject)
	assignmentReview = assignmentReview && api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ID == "" {
			continue
		}
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		if !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.Codec != "" }) ||
			track.Role != api.AudioRoleCompatibility && !trackers.StandaloneDolbyAudio(track) {
			continue
		}
		key := trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)
		inferred := trackers.AutomaticCompatibilityMix(track, candidates)
		_, answered := subject.QuestionnaireAnswers[key]
		if !answered && inferred != "" && !assignmentReview {
			continue
		}
		options, details := []string{"unresolved"}, ""
		if track.Role != api.AudioRoleCompatibility {
			options = append(options, "not_compatibility")
		}
		for _, candidate := range candidates {
			if candidate.ID == "" || candidate.Codec == "" {
				continue
			}
			options = append(options, candidate.ID)
			if details != "" {
				details += "; "
			}
			details += candidate.ID + " (" + candidate.Codec + ", " + string(candidate.Role) + ", " + candidate.Title + ")"
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      key,
			Label:    "ULCX source mix for compatibility track " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "A single same-resource TrueHD mix with the same fully known language set is matched automatically. Review ambiguous mixes using source evidence: " + details + ". Compatibility tracks for other codecs are prohibited; this answer does not waive other language or retail-source rules.",
		})
		if !answered {
			fields[len(fields)-1].Value = inferred
		}
	}
	for i := range fields {
		if value := subject.QuestionnaireAnswers[fields[i].Key]; slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
		fields[i].NeedsReview = fields[i].Required && (fields[i].Value == "unresolved" || assignmentReview && fields[i].Value != "")
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ULCX", Fields: fields}
}
