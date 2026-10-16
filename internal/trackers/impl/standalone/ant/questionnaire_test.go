// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionQuestionnaireRetainsAnsweredFields(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}}
	questionnaire := Profile().ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil || len(questionnaire.Fields) != 2 {
		t.Fatalf("expected missing type and tags fields, got %#v", questionnaire)
	}
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{
		"ANT": {
			"type":          "short",
			"tags":          "Drama",
			"adult_screens": "yes",
		},
	}
	questionnaire = projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil || len(questionnaire.Fields) != 4 {
		t.Fatalf("answered fields disappeared: %#v", questionnaire)
	}
	want := map[string]string{
		"type":          "Short Film",
		"tags":          "drama",
		"adult_screens": "yes",
	}
	for _, field := range questionnaire.Fields {
		if field.Value != want[field.Key] {
			t.Fatalf("unexpected answered field: %#v", field)
		}
	}
}

func TestProjectionQuestionnaireUsesAutomaticMetadata(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Drama"}},
	}
	if got := projectionQuestionnaire(
		trackers.PreparationInput{Meta: meta},
	); got == nil || len(got.Fields) != 1 || got.Fields[0].Key != "requestid" ||
		got.Fields[0].Required {
		t.Fatalf("automatic type and tags should need no questions: %#v", got)
	}
	meta.ProviderMetadata.TMDB.Keywords = "adult"
	got := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if got == nil || len(got.Fields) != 2 || got.Fields[0].Key != "adult_screens" || got.Fields[0].Value != "no" {
		t.Fatalf("adult screenshot consent should default to no: %#v", got)
	}
}
