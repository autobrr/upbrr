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
		if track.Kind != api.MediaTrackAudio || track.ID == "" {
			continue
		}
		candidates := compatibilityMixes(subject, track)
		if track.Role != api.AudioRoleCompatibility &&
			(!trackers.StandaloneDolbyAudio(track) || !slices.ContainsFunc(candidates, func(candidate api.MediaTrackFacts) bool { return candidate.Codec != "" })) {
			continue
		}
		key := trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID)
		if _, answered := subject.QuestionnaireAnswers[key]; !answered && trackers.AutomaticCompatibilityMix(track, candidates) != "" {
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
			Label:    "HHD source mix for compatibility track " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "A single same-resource mix with the same fully known language set is matched automatically. Review ambiguous mixes using source evidence: " + details + ". At most one compatibility track is allowed per mix; every TrueHD mix still needs standalone AC-3, including WEB releases.",
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
