// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "ULCX"), "language_secondary_retail")
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) || !slices.ContainsFunc(meta.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleCommentary || track.Role == api.AudioRoleIsolatedScore)
	}) {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ULCX", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "Retail commentary and isolated-score source",
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Value:    meta.TrackerQuestionnaireAnswers["ULCX"][key],
			Required: true,
			Help:     "Do all commentary and isolated-score tracks come from retail sources? This records source evidence, not permission to bypass a prohibited track.",
		},
	}}
}
