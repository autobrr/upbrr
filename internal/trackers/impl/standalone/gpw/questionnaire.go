// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, groupID string, answers map[string]string) *api.TrackerQuestionnaire {
	if groupID != "" {
		return nil
	}
	directorName, directorID := resolveDirectorFields(meta, answers)
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
			Value:       directorID,
			Placeholder: "nm0000138",
			Required:    true,
		},
		{
			Key:      "director_name",
			Label:    "Director Name",
			Kind:     "text",
			Value:    directorName,
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

// resolveDirectorFields pairs the selected name only with an unambiguous IMDb
// director match, preserving explicit answers when supplied.
func resolveDirectorFields(meta api.UploadSubject, answers map[string]string) (string, string) {
	name := metautil.FirstNonEmptyTrimmed(answers["director_name"], resolveDirectorName(meta))
	if id := strings.TrimSpace(answers["director_imdb"]); id != "" {
		return name, id
	}
	if name == "" || meta.ProviderMetadata.IMDB == nil {
		return name, ""
	}
	id := ""
	for _, director := range meta.ProviderMetadata.IMDB.Directors {
		candidate := strings.TrimSpace(director.ID)
		if candidate == "" || !strings.EqualFold(strings.TrimSpace(director.Name), name) {
			continue
		}
		if id != "" && id != candidate {
			return name, ""
		}
		id = candidate
	}
	return name, id
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
