// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	subject := api.NewTrackerValidationSubject(input.Meta, "LST")
	if input.Projection != nil {
		subject.FinalizedUploadName = input.Projection.UploadReleaseName
	}
	fullDisc := trackers.IsFullDiscUpload(subject.DiscType, subject.Type)
	var fields []api.TrackerQuestionnaireField
	_, assignmentReview := compatibilityAssessment(subject)
	assignmentReview = !fullDisc && assignmentReview && api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug
	for _, track := range subject.LanguageFacts.Tracks {
		if fullDisc {
			break
		}
		if track.Kind != api.MediaTrackAudio || track.ID == "" {
			continue
		}
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		if track.Role != api.AudioRoleCompatibility &&
			(!standaloneCompatibilityAudio(track) || !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.Codec != "" })) {
			continue
		}
		key := trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)
		inferred := trackers.AutomaticCompatibilityMix(track, candidates)
		answer, answered := subject.QuestionnaireAnswers[key]
		automatic := inferred != "" && !assignmentReview
		// Answered fields remain in the schema so input readiness can validate the submitted key.
		if !answered && automatic {
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
			Label:    "LST source mix for compatibility track " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug && (!automatic || answer != inferred),
			Help:     "A single same-resource TrueHD mix with the same fully known language set is matched automatically. Review ambiguous mixes using source evidence: " + details + ". DD/DD+ companions retain source-quality guidance; an answer does not waive codec or missing-audio rules.",
		})
		if !answered {
			fields[len(fields)-1].Value = inferred
		}
	}
	fields = append(fields, exceptionQuestionnaireFields(subject)...)
	for i := range fields {
		if value := subject.QuestionnaireAnswers[fields[i].Key]; fields[i].Kind == "text" || slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
		if api.NormalizeWorkflowExecutionMode(input.ExecutionMode) == api.WorkflowExecutionModeDebug {
			fields[i].Required = false
		}
		fields[i].NeedsReview = fields[i].Required &&
			(fields[i].NeedsReview || (fields[i].Value == "unresolved" || assignmentReview && strings.HasPrefix(fields[i].Key, "compatibility_mix_") && fields[i].Value != ""))
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "LST", Fields: fields}
}
