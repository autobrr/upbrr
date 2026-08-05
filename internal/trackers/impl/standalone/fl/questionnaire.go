// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, computedName string) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(meta, "FL")
	return &api.TrackerQuestionnaire{Tracker: "FL", Fields: []api.TrackerQuestionnaireField{{
		Key:      "name",
		Label:    "FileList Name",
		Kind:     "text",
		Value:    metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["name"]), computedName),
		Required: true,
	}}}
}
