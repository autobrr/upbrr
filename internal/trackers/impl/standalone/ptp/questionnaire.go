// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, groupID string) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(meta, "PTP")
	fields := make([]api.TrackerQuestionnaireField, 0, 7)
	if strings.TrimSpace(groupID) == "" {
		title, year := resolveGroupTitleYear(meta)
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:      "title",
			Label:    "Group Title",
			Kind:     "text",
			Value:    title,
			Required: true,
		}, api.TrackerQuestionnaireField{
			Key:         "year",
			Label:       "Year",
			Kind:        "text",
			Value:       year,
			Required:    false,
			Placeholder: "Release year",
		}, api.TrackerQuestionnaireField{
			Key:      "poster",
			Label:    "Poster URL",
			Kind:     "text",
			Value:    resolvePoster(meta),
			Required: true,
		}, api.TrackerQuestionnaireField{
			Key:         "tags",
			Label:       "Tags",
			Kind:        "text",
			Value:       resolveTags(meta),
			Required:    true,
			Placeholder: "Comma separated tags",
		}, api.TrackerQuestionnaireField{
			Key:         "trailer",
			Label:       "Trailer URL",
			Kind:        "text",
			Value:       resolveTrailer(meta),
			Required:    false,
			Placeholder: "YouTube trailer URL",
		}, api.TrackerQuestionnaireField{
			Key:      "album_desc",
			Label:    "Group Description",
			Kind:     "textarea",
			Value:    resolveOverview(meta),
			Required: false,
		})
	}
	if hasHardcodedSubtitles(meta) {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "hardcoded_subtitle_languages",
			Label:       "Hardcoded Subtitle Languages",
			Kind:        "text",
			Value:       strings.TrimSpace(answers["hardcoded_subtitle_languages"]),
			Placeholder: "English, French",
			Help:        "Comma-separated PTP subtitle languages",
			Required:    true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{
		Tracker: "PTP",
		Fields:  fields,
	}
}
