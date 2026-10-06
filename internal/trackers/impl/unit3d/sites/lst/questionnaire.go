// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "LST"), "trumpable_audio_eligibility")
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	facts := meta.LanguageFacts
	extra := slices.ContainsFunc(facts.ProgrammeLanguages, func(language string) bool {
		return language != "English" && !slices.Contains(facts.OriginalLanguages, language)
	})
	counts := map[string]int{}
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.Role == api.AudioRoleProgramme {
			for _, language := range track.Languages {
				counts[language]++
				if counts[language] > 1 {
					extra = true
				}
			}
		}
	}
	if !extra || len(facts.OriginalLanguages) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "LST", Fields: []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "LST conditional trumpable audio eligibility",
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Value:    meta.TrackerQuestionnaireAnswers["LST"][key],
			Required: true,
			Help:     "Does this release violate no prohibited-content rule, have an avoidable audio drawback for its intended slot, and admit a plausible replacement without losing anything important? Yes establishes those conditions only. A separate Trumpable release acknowledgement is still required; it does not apply a remote site tag or grant staff permission.",
		},
	}}
}
