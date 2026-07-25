// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(req trackers.PreparationInput) *api.TrackerQuestionnaire {
	releaseName, _ := req.ReviewedUploadName()
	return &api.TrackerQuestionnaire{
		Tracker: "THR",
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "name_override",
			Label:    "Upload Name",
			Kind:     "text",
			Value:    releaseName,
			Required: true,
		}},
	}
}
