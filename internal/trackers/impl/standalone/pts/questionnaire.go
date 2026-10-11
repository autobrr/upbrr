// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func projectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	return buildQuestionnaire(input.Meta)
}

func buildQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(meta, "PTS")
	if _, answered := answers["mandarin_override"]; hasMandarin(meta) && !answered {
		return nil
	}
	// An unanswered override must remain pending rather than default to a rejected no.
	answer := strings.ToLower(strings.TrimSpace(answers["mandarin_override"]))
	return &api.TrackerQuestionnaire{
		Tracker: "PTS",
		Fields: []api.TrackerQuestionnaireField{{
			Key:         "mandarin_override",
			Label:       "Mandarin Requirement",
			Kind:        "select",
			Options:     []string{"no", "yes"},
			Value:       answer,
			Placeholder: "Select yes or no",
			Help:        "PTS expects Mandarin audio or subtitles. Choose yes to override and upload anyway.",
			Required:    !hasMandarin(meta),
		}},
	}
}

func validateUpload(meta api.UploadSubject) string {
	if hasMandarin(meta) {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(standalone.QuestionnaireAnswers(meta, "PTS")["mandarin_override"]), "yes") {
		return ""
	}
	return "missing Mandarin audio/subtitles; answer the override questionnaire to continue"
}
