// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, fields map[string]string) *api.TrackerQuestionnaire {
	current := standalone.QuestionnaireAnswers(meta, "BJS")
	var items []api.TrackerQuestionnaireField
	if strings.TrimSpace(fields["sinopse"]) == "" {
		items = append(items, api.TrackerQuestionnaireField{
			Key:      "overview",
			Label:    "Overview",
			Kind:     "textarea",
			Value:    current["overview"],
			Required: true,
		})
	}
	if strings.TrimSpace(fields["tags"]) == "" {
		items = append(items, api.TrackerQuestionnaireField{
			Key:      "tags",
			Label:    "Tags",
			Kind:     "text",
			Value:    current["tags"],
			Required: true,
		})
	}
	if len(items) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "BJS", Fields: items}
}
