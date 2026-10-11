// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionQuestionnaireRetainsAnsweredFields(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{}
	questionnaire := Profile().ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil || len(questionnaire.Fields) != 2 {
		t.Fatalf("expected two missing metadata fields, got %#v", questionnaire)
	}
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{
		"BJS": {"overview": " Manual overview ", "tags": " drama "},
	}
	questionnaire = projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil || len(questionnaire.Fields) != 2 {
		t.Fatalf("answered fields disappeared: %#v", questionnaire)
	}
	for _, field := range questionnaire.Fields {
		want := "drama"
		if field.Key == "overview" {
			want = "Manual overview"
		}
		if field.Value != want || !field.Required {
			t.Fatalf("unexpected answered field: %#v", field)
		}
	}
}

func TestProjectionQuestionnairePreservesMetadataFallbacks(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Overview: "Provider overview", Genres: "Drama"},
	}}
	if got := projectionQuestionnaire(trackers.PreparationInput{Meta: meta}); got != nil {
		t.Fatalf("automatic metadata should need no questions: %#v", got)
	}
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{
		"BJS": {"overview": " "},
	}
	got := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if got == nil || len(got.Fields) != 1 || got.Fields[0].Value != "Provider overview" {
		t.Fatalf("explicit empty answer should retain its metadata fallback: %#v", got)
	}
}
