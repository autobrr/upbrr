// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	subject := api.NewTrackerValidationSubject(input.Meta, "LST")
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	if len(audioDefects(subject)) > 0 {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "trumpable_audio_eligibility"),
			Label:    "LST conditional trumpable audio eligibility",
			Kind:     "select",
			Options:  []string{"yes", "no"},
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Does this release violate no prohibited-content rule, have an avoidable audio drawback for its intended slot, and admit a plausible replacement without losing anything important? For secondary tracks this confirms an unjustified drawback, not a new permitted role. Yes establishes those conditions only. A separate Trumpable release acknowledgement is still required; it does not apply a remote tag or grant staff permission. No leaves eligibility unresolved because it does not identify which condition failed.",
		})
	}
	if needsSubtitlePresentation(subject) {
		options := []string{"unresolved"}
		if completeSubtitlePresentationEvidence(subject.LanguageFacts) && !hardcodedEnglishSubtitles(subject) {
			options = append([]string{"external"}, options...)
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      subtitlePresentationQuestionKey(subject),
			Label:    "LST English subtitle presentation",
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Confirm external English subtitle coverage for every programme resource lacking inspected embedded English subtitles. Known burned-in subtitles keep their hardcoded presentation; an answer cannot relabel them or clear incomplete/contradictory evidence. External coverage remains trumpable by an embedded-subtitle improvement. This does not waive the separate title-wide missing-subtitle decision.",
		})
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Role == api.AudioRoleAlternateMix {
			fields = append(fields, api.TrackerQuestionnaireField{
				Key:      trackers.LanguageQuestionKey(subject, "alternate_mix_"+track.ID),
				Label:    "LST alternate-mix source review for " + track.ID,
				Kind:     "select",
				Options:  []string{"unique", "duplicate", "unresolved"},
				Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
				Help:     "Does source review establish an additional original mix or unique remix, including mono/stereo, theatrical, Atmos/Auro3D or surround upmixes? A title alone does not prove uniqueness. A duplicate requires separate conditional trumpable review; this does not permit an otherwise prohibited dub.",
			})
		}
		if track.Role != api.AudioRoleCompatibility {
			continue
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_source_"+track.ID),
			Label:    "LST compatibility source and quality for " + track.ID,
			Kind:     "select",
			Options:  []string{"dd_core", "web_not_inferior", "web_inferior", "unresolved"},
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Is this the mix's DD core, or a WEB-sourced DD/DD+/DD+ Atmos substitute? web_not_inferior confirms source review does not establish better disc AC-3; web_inferior confirms disc-sourced AC-3 is demonstrably better and requires a separate Trumpable release acknowledgement. Unknown provenance or quality remains unresolved.",
		})
		options, details := []string{"unresolved"}, ""
		for _, candidate := range compatibilityMixes(subject.LanguageFacts, track) {
			options = append(options, candidate.ID)
			if details != "" {
				details += "; "
			}
			details += candidate.ID + " (" + candidate.Codec + ", " + string(candidate.Role) + ", " + candidate.Title + ")"
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "compatibility_mix_"+track.ID),
			Label:    "LST TrueHD mix for compatibility track " + track.ID,
			Kind:     "select",
			Options:  options,
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Help:     "Use source evidence to identify this compatibility track's TrueHD mix: " + details + ". Matching languages alone do not establish association.",
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
	return &api.TrackerQuestionnaire{Tracker: "LST", Fields: fields}
}

// Presentation answers also depend on finalized hardcoded evidence, which is
// carried separately from the aggregate language facts in the shared key.
func subtitlePresentationQuestionKey(subject api.TrackerValidationSubject) string {
	evidence := fmt.Sprintf("%s|%#v|%#v", trackers.LanguageQuestionKey(subject, "english_subtitle_presentation"),
		subject.HardcodedSubtitleLanguages, subject.HardcodedSubtitleCoverage)
	return fmt.Sprintf("english_subtitle_presentation_%x", sha256.Sum256([]byte(evidence)))
}
