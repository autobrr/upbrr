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
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}

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
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "CZT", Meta: api.UploadSubject{
				ReleaseName:                 "Example.Release.2026.1080p-GRP",
				Identity:                    api.ExternalIdentity{Category: tc.category, IMDBID: 123},
				TrackerQuestionnaireAnswers: map[string]map[string]string{"CZT": {"category": tc.answer}},
			}}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			if len(projection.Questionnaire) != 1 {
				t.Fatalf("expected category field: %#v", projection)
			}
			field := projection.Questionnaire[0]
			if field.Value != tc.want || field.Required != tc.required {
				t.Fatalf("category field = %#v, want value %q required %v", field, tc.want, tc.required)
			}
			if tc.required && tc.want == "" {
				if projection.Readiness != api.ReadinessStatusBlocked || len(projection.RequiredActions) != 1 || projection.UploadReady {
					t.Fatalf("missing category lost its corrective action: %+v", projection)
				}
			} else if !projection.UploadReady {
				t.Fatalf("selected category did not recover: %+v", projection)
			}
		})
	}
	t.Run("missing required identity", func(t *testing.T) {
		projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "CZT", Meta: api.UploadSubject{
			ReleaseName: "Example.Release.2026.1080p-GRP",
		}}, "input", "catalog", "config")
		if failure != nil || projection.Readiness != api.ReadinessStatusIneligible || len(projection.Questionnaire) != 0 {
			t.Fatalf("independent missing identity exposed category selection: %+v, %v", projection, failure)
		}
	})
}
