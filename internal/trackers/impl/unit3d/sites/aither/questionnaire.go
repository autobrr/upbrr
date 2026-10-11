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
		if !compatibilityCandidate(subject.LanguageFacts, track) {
			continue
		}
		if _, inferred := resolveCompatibilityMix(subject, track); inferred {
			continue
		}
		options := []string{"unresolved"}
		if track.Role != api.AudioRoleCompatibility {
			options = append(options, "not_compatibility")
		}
		details := ""
		for _, candidate := range compatibilityMixes(subject.LanguageFacts, track) {
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
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID),
			Label:    "AITHER TrueHD mix for standalone audio " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Review which TrueHD mix this standalone DD/DD+ track accompanies: " + details + ". A single same-resource mix with the same known languages is associated automatically unless either stream is known commentary or a distinct mix. An embedded core cannot replace the standalone track.",
		})
		if track.Role != api.AudioRoleCompatibility {
			fields[len(fields)-1].Help += " Choose not_compatibility if this stream is not a companion to a TrueHD mix."
		}
	}
	if track, ok := multilingualProgrammeTrack(subject); ok {
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
