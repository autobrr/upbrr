// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// trackMetadataQuestionKey also binds the dependent review to the selected
// exemption; a retained answer cannot establish a different recommendation scope.
func trackMetadataQuestionKey(subject api.TrackerValidationSubject) string {
	exemption := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "language_personal_exemption")]
	return trackers.LanguageQuestionKey(subject, "language_track_metadata_"+exemption)
}

func trackOrderQuestionKey(subject api.TrackerValidationSubject) string {
	exemption := subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "language_personal_exemption")]
	return trackers.LanguageQuestionKey(subject, "language_track_order_"+exemption)
}

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) || !meta.PersonalRelease {
		return nil
	}
	subject := api.NewTrackerValidationSubject(meta, "LUME")
	hasTracks := slices.ContainsFunc(subject.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
		return track.Kind == api.MediaTrackAudio || track.Kind == api.MediaTrackSubtitle
	})
	if !hasTracks {
		return nil
	}
	key := trackers.LanguageQuestionKey(subject, "language_personal_exemption")
	options := []string{"none", "trash_tier", "previously_uploaded"}
	if meta.Anime {
		options[1] = "anime_tier"
	}
	fields := []api.TrackerQuestionnaireField{
		{
			Key:      key,
			Label:    "Luminarr personal-release recommendation exemption",
			Kind:     "select",
			Options:  options,
			Value:    subject.QuestionnaireAnswers[key],
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Select an established TRaSH tier (Anime tier for anime), an older release already uploaded elsewhere, or none. Language-specific tiers do not qualify. This never waives mandatory original audio, correct track languages or complete English subtitles; other exceptions require staff permission.",
		},
	}
	if subject.QuestionnaireAnswers[key] == "none" {
		key = trackMetadataQuestionKey(subject)
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      key,
			Label:    "Luminarr personal-release track metadata",
			Kind:     "select",
			Options:  []string{"appropriate", "inappropriate", "unresolved"},
			Value:    subject.QuestionnaireAnswers[key],
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Are the audio and subtitle flags, default status and descriptive titles appropriate for their actual content? This confirms suitability that inspected metadata cannot establish. It cannot override measured omissions or contradictions, mandatory language rules, or grant staff permission.",
		})
		facts := subject.LanguageFacts
		_, orderKnown := orderedAudioTracks(facts.Tracks)
		if !orderKnown && !facts.AudioAbsent && facts.OriginalLanguagesKnown && facts.ProgrammeStatus == api.MetadataEvidenceStatusComplete {
			key = trackOrderQuestionKey(subject)
			fields = append(fields, api.TrackerQuestionnaireField{
				Key:      key,
				Label:    "Luminarr personal-release audio order",
				Kind:     "select",
				Options:  []string{"ordered", "out_of_order", "unresolved"},
				Value:    subject.QuestionnaireAnswers[key],
				Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
				Help:     "Container order could not be measured. Confirm that original audio tracks come first and normal dubs are together, English first and remaining languages alphabetically ordered. This resolves only missing order evidence, never measured violations, other language requirements or staff permission.",
			})
		}
	}
	for i := range fields {
		if !slices.Contains(fields[i].Options, fields[i].Value) {
			fields[i].Value = ""
		}
	}
	return &api.TrackerQuestionnaire{Tracker: "LUME", Fields: fields}
}
