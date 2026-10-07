// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
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
		if schema == nil || len(schema.Fields) != 1 || schema.Fields[0].Key != "trumpable_review" {
			t.Fatalf("projection leaked group fields: %#v", schema)
		}
	}
}

func TestDebugQuestionnairesPreservePayloadRequirements(t *testing.T) {
	subject := ptpLanguageSubject("Japanese", "Japanese", "English", "German")
	meta := api.UploadSubject{
		Type:           subject.Type,
		LanguageFacts:  subject.LanguageFacts,
		AudioLanguages: []string{"Japanese"},
	}
	schemas := []*api.TrackerQuestionnaire{
		buildQuestionnaire(meta, "", api.WorkflowExecutionModeDebug),
		New().TrackerAnswerSchema(trackers.PreparationInput{Meta: meta, ExecutionMode: api.WorkflowExecutionModeDebug}),
		projectionQuestionnaire(trackers.PreparationInput{Meta: meta, ExecutionMode: api.WorkflowExecutionModeDebug}),
	}
	for _, schema := range schemas {
		payload := false
		for _, field := range schema.Fields {
			if field.Key == "trumpable_review" {
				payload = true
				if !field.Required {
					t.Fatal("debug waived subtitle payload review")
				}
			}
			if strings.HasPrefix(field.Key, "programme_track_purpose_") || strings.HasPrefix(field.Key, "forced_english_dialogue_") || strings.HasPrefix(field.Key, "english_subtitle_manager_") {
				t.Fatalf("source or hypothetical subtitle question remains: %+v", field)
			}
		}
		if !payload {
			t.Fatalf("fixture missing payload evidence: %+v", schema)
		}
	}
}

func TestPTPProjectionHidesResolvedAutoSubtitleControl(t *testing.T) {
	meta := api.UploadSubject{AudioLanguages: []string{"English"}, LanguageFacts: ptpLanguageSubject("English", "English").LanguageFacts}
	questionnaire := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire != nil && slices.ContainsFunc(questionnaire.Fields, func(field api.TrackerQuestionnaireField) bool {
		return field.Key == "no_english_subtitles" || strings.HasPrefix(field.Key, "forced_english_dialogue_")
	}) {
		t.Fatalf("resolved English audio acquired optional or hypothetical questions: %+v", questionnaire)
	}
}

func TestPTPProjectionPreservesExplicitSubtitleOverridesAndConflicts(t *testing.T) {
	for _, test := range []struct {
		answer   string
		value    string
		required bool
	}{
		{"no", "no", false},
		{"yes", "", true},
		{"invalid", "", true},
	} {
		meta := api.UploadSubject{
			AudioLanguages:              []string{"English"},
			SubtitleLanguages:           []string{"English"},
			LanguageFacts:               ptpLanguageSubject("English", "English").LanguageFacts,
			TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": {"no_english_subtitles": test.answer}},
		}
		schema := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
		if schema == nil || len(schema.Fields) != 1 || schema.Fields[0].Key != "no_english_subtitles" || schema.Fields[0].Value != test.value || schema.Fields[0].Required != test.required {
			t.Fatalf("explicit subtitle answer %q lost its editable control: %+v", test.answer, schema)
		}
	}
}
