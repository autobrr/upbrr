// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, fields map[string][]string) *api.TrackerQuestionnaire {
	current := standalone.QuestionnaireAnswers(meta, "BT")
	var items []api.TrackerQuestionnaireField

	sinopse := ""
	if len(fields["sinopse"]) > 0 {
		sinopse = strings.TrimSpace(fields["sinopse"][0])
	}
	if sinopse == "" {
		items = append(items, api.TrackerQuestionnaireField{
			Key:      "overview",
			Label:    "Overview",
			Kind:     "textarea",
			Value:    current["overview"],
			Required: true,
		})
	}

	tags := ""
	if len(fields["tags"]) > 0 {
		tags = strings.TrimSpace(fields["tags"][0])
	}
	if tags == "" {
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
	return &api.TrackerQuestionnaire{Tracker: "BT", Fields: items}
}
