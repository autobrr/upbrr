// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func projectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	answers := standalone.QuestionnaireAnswers(input.Meta, "ANT")
	typeName, _ := resolveType(input.Meta, answers)
	tags, _ := resolveTags(input.Meta, answers)
	return buildQuestionnaire(input.Meta, uploadState{
		typeName:     typeName,
		tags:         tags,
		adultContent: detectAdult(input.Meta),
	})
}

func buildQuestionnaire(meta api.UploadSubject, state uploadState) *api.TrackerQuestionnaire {
	current := standalone.QuestionnaireAnswers(meta, "ANT")
	fields := make([]api.TrackerQuestionnaireField, 0, 3)
	if _, answered := current["type"]; strings.TrimSpace(state.typeName) == "" || answered {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "type",
			Label:       "ANT Type",
			Kind:        "select",
			Options:     []string{"Feature Film", "Short Film", "Miniseries", "Other"},
			Value:       metautil.FirstNonEmptyTrimmed(state.typeName, current["type"]),
			Placeholder: "Select a release type",
			Help:        "Pick the ANT content type for this release",
			Required:    true,
		})
	}
	if _, answered := current["tags"]; strings.TrimSpace(state.tags) == "" || answered {
		// ANT can obtain genres from TMDB when an explicit tag value is omitted.
		required := meta.ProviderMetadata.TMDB == nil || genreTags(meta.ProviderMetadata.TMDB.Genres) == ""
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "tags",
			Label:       "Tags",
			Kind:        "text",
			Value:       state.tags,
			Placeholder: "action, drama",
			Help:        "Comma-separated ANT tags",
			Required:    required,
		})
	}
	if _, answered := current["adult_screens"]; state.adultContent || answered {
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         "adult_screens",
			Label:       "Upload Screenshots",
			Kind:        "select",
			Options:     []string{"no", "yes"},
			Value:       metautil.FirstNonEmptyTrimmed(strings.TrimSpace(current["adult_screens"]), "no"),
			Placeholder: "Select yes or no",
			Help:        "Set to yes to include screenshots for adult content",
			Required:    true,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "ANT", Fields: fields}
}
