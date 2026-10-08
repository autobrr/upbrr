// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

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
	answers := standalone.QuestionnaireAnswers(meta, "ASC")
	overview := strings.TrimSpace(resolveOverview(meta, answers))
	genres := strings.TrimSpace(resolveGenres(meta, answers))
	fields := make([]api.TrackerQuestionnaireField, 0, 2)
	if _, answered := answers["overview"]; overview == "" || answered {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      "overview",
			Label:    "Sinopse",
			Kind:     "textarea",
			Value:    overview,
			Required: true,
		})
	}
	if _, answered := answers["genre"]; len(resolveGenreIDs(genres)) == 0 || answered {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "genre",
			Label:       "Gêneros",
			Kind:        "text",
			Value:       genres,
			Placeholder: "Drama, Ação",
			Required:    true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ASC", Fields: fields}
}
