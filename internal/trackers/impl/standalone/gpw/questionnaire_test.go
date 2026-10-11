// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestTrackerAnswerSchemaDefersUnknownGroupRequirements(t *testing.T) {
	if schema := New().ProjectionQuestionnaire(trackers.PreparationInput{}); schema != nil {
		t.Fatal("normal projection exposed speculative new-group fields")
	}
	for _, imdb := range []int{0, 123} {
		meta := api.UploadSubject{Identity: api.ExternalIdentity{IMDBID: imdb}, TrackerQuestionnaireAnswers: map[string]map[string]string{"GPW": {"director_imdb": "nm0000123"}}}
		schema := New().TrackerAnswerSchema(trackers.PreparationInput{Meta: meta})
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

func TestNewGroupDirectorIDMatchesSelectedName(t *testing.T) {
	for _, test := range []struct {
		name      string
		directors []api.IMDBPerson
		answers   map[string]string
		want      string
	}{
		{
			name:      "matching second director",
			directors: []api.IMDBPerson{{ID: "nm0000001", Name: "Other Director"}, {ID: "nm0000002", Name: "Selected Director"}},
			want:      "nm0000002",
		},
		{name: "different director", directors: []api.IMDBPerson{{ID: "nm0000001", Name: "Other Director"}}},
		{name: "ambiguous name", directors: []api.IMDBPerson{{ID: "nm0000001", Name: "Selected Director"}, {ID: "nm0000002", Name: "Selected Director"}}},
		{
			name:      "manual name",
			directors: []api.IMDBPerson{{ID: "nm0000001", Name: "Other Director"}},
			answers:   map[string]string{"director_name": "Other Director"},
			want:      "nm0000001",
		},
		{
			name:    "explicit id",
			answers: map[string]string{"director_imdb": "nm0000003"},
			want:    "nm0000003",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{Directors: []string{"Selected Director"}},
				IMDB: &api.IMDBMetadata{Directors: test.directors},
			}}
			schema := buildQuestionnaire(meta, "", test.answers)
			fields := buildFields(trackers.PreparationInput{Meta: meta}, config.TrackerConfig{}, "description", "", test.answers)
			if schema.Fields[1].Value != test.want || fields["artist_ids[]"] != test.want || schema.Fields[2].Value != fields["artists[]"] {
				t.Fatalf("director schema=%+v payload=%q/%q", schema.Fields[1:3], fields["artists[]"], fields["artist_ids[]"])
			}
		})
	}
}
