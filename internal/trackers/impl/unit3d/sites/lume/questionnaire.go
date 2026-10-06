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
	key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "LUME"), "language_personal_exemption")
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) || !meta.PersonalRelease {
		return nil
	}

	needsExemption := slices.ContainsFunc(languageAssessment(api.NewTrackerValidationSubject(meta, "LUME")), func(failure api.RuleFailure) bool {
		return failure.Rule == "language_original_order" || failure.Rule == "language_dub_order"
	})
	if !needsExemption {
		return nil
	}

	options := []string{"none", "trash_tier", "previously_uploaded"}
	if meta.Anime {
		options[1] = "anime_tier"
	}
	return &api.TrackerQuestionnaire{Tracker: "LUME", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "Luminarr personal-release recommendation exemption",
			Kind:     "select",
			Options:  options,
			Value:    meta.TrackerQuestionnaireAnswers["LUME"][key],
			Required: true,
			Help:     "Select an established TRaSH tier (Anime tier for anime), an older release already uploaded elsewhere, or none. Language-specific tiers do not qualify. This never waives mandatory original audio or complete English subtitles; other exceptions require staff permission.",
		},
	}}
}
