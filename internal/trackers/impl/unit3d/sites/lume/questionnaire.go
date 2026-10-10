// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) || !meta.PersonalRelease {
		return nil
	}
	subject := api.NewTrackerValidationSubject(meta, "LUME")
	failures := trackMetadataFailures(subject, trackers.LanguageProhibited)
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool { return failure.Disposition == api.RuleDispositionStrict }) {
		return nil
	}
	key := trackers.LanguageQuestionKey(subject, "language_personal_exemption")
	options := []string{"none", "trash_tier", "previously_uploaded"}
	if meta.Anime {
		options[1] = "anime_tier"
	}
	value := subject.QuestionnaireAnswers[key]
	if !slices.Contains(options, value) {
		value = ""
	}
	return &api.TrackerQuestionnaire{Tracker: "LUME", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "Luminarr personal-release recommendation exemption",
			Kind:     "select",
			Options:  options,
			Value:    value,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Select an established TRaSH tier (Anime tier for anime), an older release already uploaded elsewhere, or none. Language-specific tiers do not qualify. This never waives mandatory original audio, correct track languages or complete English subtitles; other exceptions require staff permission.",
		},
	}}
}
