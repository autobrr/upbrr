// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import "github.com/autobrr/upbrr/pkg/api"

func exceptionQuestionnaireFields(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	kind := containerException(subject)
	if kind == "" {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	if kind == "legacy" {
		key := exceptionQuestionKey(subject, "sole_source")
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      key,
			Label:    "LST legacy source availability",
			Kind:     "select",
			Required: true,
			Options:  []string{"unresolved", "sole_source_verified", "other_version_exists"},
			Value:    subject.QuestionnaireAnswers[key],
			Help:     "Verify title-wide availability, every search result, and source history in every format. An empty technical-slot search is insufficient. Select sole_source_verified only when this is the sole known source for older content or preservation.",
		})
	}
	key := exceptionQuestionKey(subject, "exception_note")
	help := "Explain the applicable container exception. The explanation is included in the upload description and the upload is placed in moderation."
	if kind == "legacy" {
		help = "Explain why this older release or preservation copy is the sole known source in any format, including the source and search evidence you checked. The upload is placed in moderation."
	}
	fields = append(fields, api.TrackerQuestionnaireField{
		Key:      key,
		Label:    "LST container exception explanation",
		Kind:     "text",
		Required: true,
		Value:    subject.QuestionnaireAnswers[key],
		Help:     help,
	})
	return fields
}
