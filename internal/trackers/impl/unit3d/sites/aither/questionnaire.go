// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	subject := api.NewTrackerValidationSubject(input.Meta, "AITHER")
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleCompatibility {
			continue
		}
		options := []string{"unresolved"}
		details := ""
		for _, candidate := range compatibilityMixes(subject.LanguageFacts, track) {
			options = append(options, candidate.ID)
			if details != "" {
				details += "; "
			}
			details += candidate.ID + " (" + candidate.Codec + ", " + string(candidate.Role) + ", " + candidate.Title + ")"
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID),
			Label:    "AITHER TrueHD mix for compatibility track " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Use source evidence to identify the TrueHD mix this standalone DD/DD+ track accompanies: " + details + ". Matching languages alone do not establish association, and an embedded core cannot replace the standalone track.",
		})
	}
	if track, ok := multilingualProgrammeTrack(subject.LanguageFacts); ok {
		options := []string{"evenly_split", "unresolved"}
		for _, language := range track.Languages {
			options = append(options, "predominant:"+language)
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "multilingual_balance_"+track.ID),
			Label:    "AITHER language balance for programme track " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: true,
			Help:     "Review the spoken content: is this one track evenly split between its identified languages, or does one language predominate? Only source-confirmed evenly split content uses MULTIPLE LANGUAGES. A list of languages or their order does not establish balance. This changes naming only and does not permit otherwise prohibited dubs.",
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
	return &api.TrackerQuestionnaire{Tracker: "AITHER", Fields: fields}
}
