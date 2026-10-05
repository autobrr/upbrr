// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestProjectionQuestionnaireDefersUnknownGroupRequirements(t *testing.T) {
	for _, imdb := range []int{0, 123} {
		meta := api.UploadSubject{Identity: api.ExternalIdentity{IMDBID: imdb}, TrackerQuestionnaireAnswers: map[string]map[string]string{"GPW": {"director_imdb": "nm0000123"}}}
		schema := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
		if len(schema.Fields) != 5 {
			t.Fatalf("schema=%+v", schema)
		}
		for _, field := range schema.Fields {
			if field.Key == "director_imdb" && (field.Value != "nm0000123" || field.Required != (imdb == 0)) {
				t.Fatalf("imdb=%d field=%+v", imdb, field)
			}
		}
	}
	if schema := buildQuestionnaire(api.UploadSubject{}, "123", nil); schema != nil {
		t.Fatal("existing group required irrelevant new-group data")
	}
	if validateFields("", nil) == "" || validateFields("123", nil) != "" {
		t.Fatal("remote group validation changed")
	}
}

func TestNewGroupPosterDefaultMatchesQuestionnaireWithoutPromotingAnswers(t *testing.T) {
	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Poster: "https://images.example/poster.jpg"}}}
	schema := buildQuestionnaire(meta, "", nil)
	fields := buildFields(trackers.PreparationInput{Meta: meta}, config.TrackerConfig{}, "description", "", nil)
	if schema.Fields[0].Value == "" || fields["image"] != schema.Fields[0].Value {
		t.Fatalf("poster default=%q payload=%q", schema.Fields[0].Value, fields["image"])
	}
}
