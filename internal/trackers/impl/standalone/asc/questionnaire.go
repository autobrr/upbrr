// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(meta, "ASC")
	fields := make([]api.TrackerQuestionnaireField, 0, 2)
	if strings.TrimSpace(resolveOverview(meta, answers)) == "" {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      "overview",
			Label:    "Sinopse",
			Kind:     "textarea",
			Value:    strings.TrimSpace(answers["overview"]),
			Required: true,
		})
	}
	if strings.TrimSpace(resolveGenres(meta, answers)) == "" {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "genre",
			Label:       "Gêneros",
			Kind:        "text",
			Value:       strings.TrimSpace(answers["genre"]),
			Placeholder: "Drama, Action",
			Required:    true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ASC", Fields: fields}
}
