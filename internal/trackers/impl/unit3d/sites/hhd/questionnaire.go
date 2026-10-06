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
	if needsSubtitleManagerReview(subject.LanguageFacts) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "english_subtitle_manager"),
			Label:    "HHD English subtitles in subtitle manager",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  []string{"available", "missing", "unresolved"},
			Help:     "Are English subtitles matching this release available through HHD's subtitle manager? English subtitles are required even with an English dub. This records manager coverage without changing measured local tracks or applying a remote tag.",
		})
	}
	if !webSourceVideo(subject) {
		options := []string{"retained", "incomplete", "unresolved"}
		if !discSourceVideo(subject) {
			options = append(options, "no_disc_source")
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "source_disc_audio"),
			Label:    "HHD primary-disc audio retention",
			Kind:     "select",
			Required: subject.PersonalRelease && api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  options,
			Help:     "Confirm that original mixes, commentary and unique isolated scores/music present on the primary source discs were retained. These should be included and are mandatory for personal releases. Select retained also when the reviewed disc contains none of this material; no_disc_source only when there is no primary disc source. Equivalent material from additional sources is recommended. Unknown source content remains unresolved.",
		})
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleCompatibility {
			continue
		}
		options := []string{"not_duplicated_core", "duplicated_core", "unresolved"}
		if webSourceVideo(subject) {
			options = append(options, "untouched_hls")
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_source_"+track.ID),
			Label:    "HHD compatibility source for " + track.ID + " (" + track.Codec + ")",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  options,
			Help:     "Review this track's source: not_duplicated_core confirms it is not a separately duplicated embedded core. For web video, untouched_hls confirms unchanged audio from the HLS source, needed for compatibility and not a duplicate embedded core. If the video source is unknown, correct Source in Input before reviewing an HLS exception. This cannot replace a TrueHD mix's required standalone AC-3 or waive another codec, count or language rule.",
		})
		candidates := compatibilityMixes(subject.LanguageFacts, track)
		if len(candidates) <= 1 {
			continue
		}
		options = []string{"unresolved"}
		details := ""
		for _, candidate := range candidates {
			options = append(options, candidate.ID)
			if details != "" {
				details += "; "
			}
			details += candidate.ID + " (" + candidate.Codec + ", " + string(candidate.Role) + ", " + candidate.Title + ")"
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID),
			Label:    "HHD source mix for compatibility track " + track.ID,
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  options,
			Help:     "Use source evidence to select this track's corresponding mix: " + details + ". Matching languages or codec counts alone do not establish association. At most one compatibility track is allowed per mix; every TrueHD mix still needs standalone AC-3.",
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
