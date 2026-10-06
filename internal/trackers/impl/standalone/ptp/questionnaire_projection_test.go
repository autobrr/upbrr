// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestTrackerAnswerSchemaPreservesGroupAnswersAndLegacyIntent(t *testing.T) {
	for _, imdb := range []int{0, 123} {
		answers := map[string]string{
			"title":                "Reviewed title",
			"year":                 "2025",
			"poster":               "https://images.example/poster.jpg?size=original",
			"tags":                 "drama",
			"trailer":              "https://www.youtube.com/watch?v=EXAMPLE",
			"album_desc":           "Reviewed description",
			"no_english_subtitles": "no",
		}
		meta := api.UploadSubject{Identity: api.ExternalIdentity{IMDBID: imdb}, TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": answers}}
		schema := New().TrackerAnswerSchema(trackers.PreparationInput{Meta: meta})
		for _, field := range schema.Fields {
			if want, exists := answers[field.Key]; exists && field.Value != want {
				t.Fatalf("lost %s answer: %q want %q", field.Key, field.Value, want)
			}
			if field.Key == "poster" && field.Required != (imdb == 0) {
				t.Fatalf("guessed remote group: %+v", field)
			}
		}
	}
}

func TestLegacySubtitleConflictRemainsTrackerLocal(t *testing.T) {
	meta := api.UploadSubject{SubtitleLanguages: []string{"English"}, TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": {"no_english_subtitles": "yes"}}}
	if outcomes := New().InputReadiness(meta); len(outcomes) != 0 {
		t.Fatalf("tracker answer blocked canonical Input: %+v", outcomes)
	}
	field := legacySubtitleField(meta)
	if !field.Required || field.Value != "" || validateNoEnglishSubtitles(meta) == "" {
		t.Fatalf("conflict lost: %+v", field)
	}
}

func TestProjectionQuestionnaireContainsOnlySubtitleReview(t *testing.T) {
	for _, imdb := range []int{0, 123} {
		meta := api.UploadSubject{Identity: api.ExternalIdentity{IMDBID: imdb}, AudioLanguages: []string{"French"}}
		schema := New().ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
		if schema == nil || len(schema.Fields) != 2 || schema.Fields[0].Key != "trumpable_review" || schema.Fields[1].Key != "no_english_subtitles" {
			t.Fatalf("projection leaked group fields: %#v", schema)
		}
	}
}

func TestDebugQuestionnairesPreservePayloadRequirements(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "English", "German")
	meta := api.UploadSubject{
Type: subject.Type,
 LanguageFacts: subject.LanguageFacts,
 AudioLanguages: []string{"Japanese"},
}
	schemas := []*api.TrackerQuestionnaire{
		buildQuestionnaire(meta, "", api.WorkflowExecutionModeDebug),
		New().TrackerAnswerSchema(trackers.PreparationInput{Meta: meta, ExecutionMode: api.WorkflowExecutionModeDebug}),
		projectionQuestionnaire(trackers.PreparationInput{Meta: meta, ExecutionMode: api.WorkflowExecutionModeDebug}),
	}
	for _, schema := range schemas {
		payload, language := false, false
		for _, field := range schema.Fields {
			if field.Key == "trumpable_review" {
				payload = true
				if !field.Required {
					t.Fatal("debug waived subtitle payload review")
				}
			}
			if strings.HasPrefix(field.Key, "programme_track_purpose_") || strings.HasPrefix(field.Key, "forced_english_dialogue_") {
				language = true
				if field.Required {
					t.Fatalf("debug re-blocked eligibility through %s", field.Key)
				}
			}
		}
		if !payload || !language {
			t.Fatalf("fixture missing payload/language evidence: %+v", schema)
		}
	}
}
