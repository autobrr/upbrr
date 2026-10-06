// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func languageQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	meta := input.Meta
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) {
		return nil
	}
	subject := api.NewTrackerValidationSubject(meta, "BTN")
	var fields []api.TrackerQuestionnaireField
	language := btnPrimaryProgrammeLanguage(meta.LanguageFacts)
	if language != "" && !isBTNEnglishLanguage(language) {
		key := trackers.LanguageQuestionKey(subject, "primary_audio_country")
		value := subject.QuestionnaireAnswers[key]
		if btnPrimaryCountryID(subject) == "" {
			value = ""
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      key,
			Label:    "BTN country for primary " + language + " audio",
			Kind:     "text",
			Value:    value,
			Required: true,
			Help:     "Enter the BTN country matching the primary programme audio, using a supported country name or code. The work's origin country does not establish a dub's country. This does not authorize staff-only or prohibited content.",
		})
	}
	if strings.EqualFold(meta.Type, "REMUX") {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "source_audio"),
			Label:    "BTN remux source audio",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  []string{"best_original_retained", "incomplete", "unresolved"},
			Help:     "Compare available sources: does the remux retain the best original-language primary audio? A codec, language label or default flag alone does not establish best-available source retention. Known omissions are prohibited.",
		})
	}
	if discSourceVideo(subject) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "source_extras"),
			Label:    "BTN source-disc extras",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  []string{"retained_or_unavailable", "incomplete", "unresolved"},
			Help:     "Available source-disc commentary, isolated scores and chapters must be included. Select retained_or_unavailable after verifying all available material is included or none exists. Retail alternate mixes and supplementary tracks are permitted; titles alone do not establish retail provenance.",
		})
	}
	if bluRaySourceVideo(subject) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      trackers.LanguageQuestionKey(subject, "broadcast_soundtrack"),
			Label:    "BTN Blu-ray and broadcast soundtracks",
			Kind:     "select",
			Required: api.NormalizeWorkflowExecutionMode(input.ExecutionMode) != api.WorkflowExecutionModeDebug,
			Options:  []string{"same_or_both_retained", "missing", "unresolved"},
			Help:     "If the Blu-ray soundtrack differs from the original broadcast soundtrack, both should be retained. Select same_or_both_retained after comparison, missing when one of the differing pair is omitted, or unresolved. Omission creates a Trumpable release finding requiring a separate acknowledgement; it does not automatically qualify another upload as a replacement.",
		})
	}
	if needsRetailEnglishDubReview(subject) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:     trackers.LanguageQuestionKey(subject, "retail_english_dub"),
			Label:   "BTN retail English dub for foreign animation",
			Kind:    "select",
			Options: []string{"unavailable", "available_missing", "unresolved"},
			Help:    "Foreign animation should include an English dub when available from retail sources. This is guidance. English dubs of foreign live action are optional; foreign-language and English versions can coexist. Optional dubs, subtitles or chapters alone do not qualify an identically sourced WEB-DL to replace another without staff approval.",
		})
	}
	if strings.EqualFold(meta.Type, "REMUX") && foreignOriginal(meta.LanguageFacts) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:     trackers.LanguageQuestionKey(subject, "retail_english_subtitles"),
			Label:   "BTN remux retail English subtitles",
			Kind:    "select",
			Options: []string{"retail_included", "unavailable", "available_missing", "unresolved"},
			Help:    "Retail English subtitles can improve a foreign remux. Compare the retail source and the upload before declaring them included, unavailable or missing. This records source evidence and does not grant automatic replacement eligibility or waive anime's subtitle requirement.",
		})
	}
	for i := range fields {
		if fields[i].Kind != "select" {
			continue
		}
		value := subject.QuestionnaireAnswers[fields[i].Key]
		if slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "BTN", Fields: fields}
}
