// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionQuestionnaireOmitsUnsupportedContentTypes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		providerType string
		answer       string
	}{
		{providerType: "tv series"},
		{providerType: "tv mini series", answer: "Short Film"},
		{providerType: "tvEpisode", answer: "Feature Film"},
		{providerType: "tv short"},
		{providerType: "tvShort", answer: "Short Film"},
	} {
		t.Run(test.providerType, func(t *testing.T) {
			meta := api.UploadSubject{
				Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				ProviderMetadata: api.SourceScopedMetadata{
					IMDB: &api.IMDBMetadata{Type: test.providerType},
				},
				TrackerQuestionnaireAnswers: map[string]map[string]string{"ANT": {"type": test.answer}},
			}
			questionnaire := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
			if slices.ContainsFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool {
				return field.Key == "type"
			}) {
				t.Fatalf("unsupported content must not offer a type override: %#v", questionnaire)
			}
			subject := api.NewTrackerValidationSubject(meta, "ANT")
			subject.MediaInfoTextReady = true
			failures, err := Profile().ValidationPolicy.Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(failures) != 1 || failures[0].Rule != "unsupported_type" || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("explicit unsupported IMDb type must remain blocked: %+v", failures)
			}
		})
	}
}

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
