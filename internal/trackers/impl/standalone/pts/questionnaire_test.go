// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pts

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionQuestionnaireRequiresExplicitMandarinOverride(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		answers map[string]string
		want    string
		valid   bool
	}{
		{name: "unanswered"},
		{
			name:    "declined",
			answers: map[string]string{"mandarin_override": "no"},
			want:    "no",
		},
		{
			name:    "approved",
			answers: map[string]string{"mandarin_override": " YES "},
			want:    "yes",
			valid:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{"PTS": tc.answers}}
			questionnaire := Profile().ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
			if questionnaire == nil || len(questionnaire.Fields) != 1 {
				t.Fatalf("expected Mandarin question: %#v", questionnaire)
			}
			field := questionnaire.Fields[0]
			if field.Value != tc.want || !field.Required {
				t.Fatalf("unexpected Mandarin question: %#v", field)
			}
			if got := validateUpload(meta) == ""; got != tc.valid {
				t.Fatalf("Mandarin validation accepted = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestProjectionQuestionnaireRetainsMandarinAnswerWithNewLanguageEvidence(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{AudioLanguages: []string{"Mandarin"}}
	if got := projectionQuestionnaire(trackers.PreparationInput{Meta: meta}); got != nil {
		t.Fatalf("Mandarin release should need no override: %#v", got)
	}
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"PTS": {"mandarin_override": "yes"}}
	got := projectionQuestionnaire(trackers.PreparationInput{Meta: meta})
	if got == nil || len(got.Fields) != 1 || got.Fields[0].Value != "yes" || got.Fields[0].Required {
		t.Fatalf("retained optional Mandarin answer = %#v", got)
	}
}
