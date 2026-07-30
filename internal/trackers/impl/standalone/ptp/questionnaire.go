// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func buildQuestionnaire(meta api.UploadSubject, groupID string) *api.TrackerQuestionnaire {
	if strings.TrimSpace(groupID) != "" {
		return nil
	}
	title, year := resolveGroupTitleYear(meta)
	fields := []api.TrackerQuestionnaireField{
		{
			Key:      "title",
			Label:    "Group Title",
			Kind:     "text",
			Value:    title,
			Required: true,
		},
		{
			Key:         "year",
			Label:       "Year",
			Kind:        "text",
			Value:       year,
			Required:    false,
			Placeholder: "Release year",
		},
		{
			Key:      "poster",
			Label:    "Poster URL",
			Kind:     "text",
			Value:    resolvePoster(meta),
			Required: true,
		},
		{
			Key:         "tags",
			Label:       "Tags",
			Kind:        "text",
			Value:       resolveTags(meta),
			Required:    true,
			Placeholder: "Comma separated tags",
		},
		{
			Key:         "trailer",
			Label:       "Trailer URL",
			Kind:        "text",
			Value:       resolveTrailer(meta),
			Required:    false,
			Placeholder: "YouTube trailer URL",
		},
		{
			Key:      "album_desc",
			Label:    "Group Description",
			Kind:     "textarea",
			Value:    resolveOverview(meta),
			Required: false,
		},
	}
	return &api.TrackerQuestionnaire{
		Tracker: "PTP",
		Fields:  fields,
	}
}
