// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func projectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	return buildQuestionnaire(input.Meta)
}

func buildQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	current := standalone.QuestionnaireAnswers(meta, "BJS")
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	overview := metautil.FirstNonEmptyTrimmed(current["overview"], resolveOverview(meta, ptBR))
	tags := metautil.FirstNonEmptyTrimmed(current["tags"], resolveTags(meta, ptBR))
	var items []api.TrackerQuestionnaireField
	if _, answered := current["overview"]; overview == "" || answered {
		items = append(items, api.TrackerQuestionnaireField{
			Key:      "overview",
			Label:    "Overview",
			Kind:     "textarea",
			Value:    overview,
			Required: true,
		})
	}
	if _, answered := current["tags"]; tags == "" || answered {
		items = append(items, api.TrackerQuestionnaireField{
			Key:      "tags",
			Label:    "Tags",
			Kind:     "text",
			Value:    tags,
			Required: true,
		})
	}
	if len(items) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "BJS", Fields: items}
}
