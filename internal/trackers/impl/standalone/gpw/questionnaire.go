// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, groupID string, answers map[string]string) *api.TrackerQuestionnaire {
	if groupID != "" {
		return nil
	}
	fields := []api.TrackerQuestionnaireField{
		{
			Key:      "poster_url",
			Label:    "Poster URL",
			Kind:     "text",
			Value:    metautil.FirstNonEmptyTrimmed(answers["poster_url"], resolvePoster(meta)),
			Required: true,
		},
		{
			Key:         "director_imdb",
			Label:       "Director IMDb ID",
			Kind:        "text",
			Value:       answers["director_imdb"],
			Placeholder: "nm0000138",
			Required:    true,
		},
		{
			Key:      "director_name",
			Label:    "Director Name",
			Kind:     "text",
			Value:    metautil.FirstNonEmptyTrimmed(answers["director_name"], resolveDirectorName(meta)),
			Required: true,
		},
		{
			Key:   "director_chinese",
			Label: "Director Chinese",
			Kind:  "text",
			Value: answers["director_chinese"],
		},
		{
			Key:      "tags",
			Label:    "Tags",
			Kind:     "text",
			Value:    metautil.FirstNonEmptyTrimmed(answers["tags"], resolveTags(meta)),
			Required: true,
		},
	}
	return &api.TrackerQuestionnaire{Tracker: "GPW", Fields: fields}
}

// TrackerAnswerSchema accepts legacy group inputs without publishing speculative
// group requirements before remote upload preparation determines applicability.
func (d *Definition) TrackerAnswerSchema(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	questionnaire := buildQuestionnaire(input.Meta, "", standalone.QuestionnaireAnswers(input.Meta, "GPW"))
	for index := range questionnaire.Fields {
		field := &questionnaire.Fields[index]
		field.Required = input.Meta.Identity.IMDBID == 0 && field.Required
		field.Help = "Used only when creating a new GPW group. Existing-group uploads ignore this field; group lookup runs during upload preparation."
	}
	return questionnaire
}
