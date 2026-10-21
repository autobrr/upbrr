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
		{name: "invalid saved answer", answers: map[string]string{"mandarin_override": "invalid"}},
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

func TestProjectionMandarinOverrideReadiness(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{"", "invalid", "no", "yes"} {
		projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "PTS", Meta: api.UploadSubject{
			ReleaseName: "Example.Movie.2026.1080p-GRP",
			TrackerQuestionnaireAnswers: map[string]map[string]string{"PTS": {"mandarin_override": answer}},
		}}, "input", "catalog", "config")
		if failure != nil {
			t.Fatal(failure)
		}
		switch answer {
		case "no":
			if projection.Readiness != api.ReadinessStatusIneligible || len(projection.Questionnaire) != 0 || len(projection.RequiredActions) != 0 {
				t.Fatalf("declined override requested more input: %+v", projection)
			}
		case "yes":
			if !projection.UploadReady || len(projection.RequiredActions) != 0 {
				t.Fatalf("approved override remained blocked: %+v", projection)
			}
		default:
			if projection.Readiness != api.ReadinessStatusBlocked || len(projection.Questionnaire) != 1 ||
				projection.Questionnaire[0].Value != "" || len(projection.RequiredActions) != 1 {
				t.Fatalf("missing or invalid override lost corrective input: %+v", projection)
			}
		}
	}
}
