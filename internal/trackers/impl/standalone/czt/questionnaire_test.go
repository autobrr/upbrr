// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionQuestionnaireUsesResolvedCategory(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		category api.CanonicalCategory
		answer   string
		want     string
		required bool
	}{
		{
			name:     "automatic movie",
			category: api.CanonicalCategoryMovie,
			want:     "Movies/HD",
		},
		{
			name:     "named override",
			category: api.CanonicalCategoryMovie,
			answer:   " music/lossless ",
			want:     "Music/Lossless",
		},
		{
			name:     "numeric override",
			category: api.CanonicalCategoryMovie,
			answer:   "35",
			want:     "Music/Lossless",
		},
		{name: "unclassified", required: true},
		{
			name:     "explicit non-video",
			answer:   "22",
			want:     "Software",
			required: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			questionnaire := Profile().ProjectionQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{
				Identity:                    api.ExternalIdentity{Category: tc.category},
				TrackerQuestionnaireAnswers: map[string]map[string]string{"CZT": {"category": tc.answer}},
			}})
			if questionnaire == nil || len(questionnaire.Fields) != 1 {
				t.Fatalf("expected category field: %#v", questionnaire)
			}
			field := questionnaire.Fields[0]
			if field.Value != tc.want || field.Required != tc.required {
				t.Fatalf("category field = %#v, want value %q required %v", field, tc.want, tc.required)
			}
		})
	}
}
