// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionOptionalRequestIDCorrection(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		category api.CanonicalCategory
		imdbType string
		request  string
		review   bool
	}{
		{
			name:     "movie short",
			category: api.CanonicalCategoryMovie,
			imdbType: "short",
		},
		{
			name:     "movie request zero",
			category: api.CanonicalCategoryMovie,
			imdbType: "short",
			request:  "0",
			review:   true,
		},
		{
			name:     "movie request text",
			category: api.CanonicalCategoryMovie,
			imdbType: "short",
			request:  "text",
			review:   true,
		},
		{
			name:     "movie request cleared",
			category: api.CanonicalCategoryMovie,
			imdbType: "short",
			request:  "",
		},
		{
			name:     "movie request valid",
			category: api.CanonicalCategoryMovie,
			imdbType: "short",
			request:  "42",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker: "ANT",
				Meta: api.UploadSubject{
					Assessments:                 api.ReleaseAssessments{MediaInfoUniqueID: api.UniqueIDStatusPresent},
					ReleaseName:                 "Example.Short.2026.1080p.WEB-DL.x264-GRP",
					MediaInfoTextPath:           filepath.Join(t.TempDir(), "mediainfo.txt"),
					Identity:                    api.ExternalIdentity{Category: test.category, TMDBID: 123},
					TrackerQuestionnaireAnswers: map[string]map[string]string{"ANT": {"requestid": test.request}},
					ProviderMetadata: api.SourceScopedMetadata{
						IMDB: &api.IMDBMetadata{Type: test.imdbType},
						TMDB: &api.TMDBMetadata{TMDBID: 123, Genres: "Drama"},
					},
				},
			}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			if len(projection.Questionnaire) != 1 || projection.Questionnaire[0].NeedsReview != test.review ||
				projection.Questionnaire[0].Key != "requestid" || projection.Questionnaire[0].Required {
				t.Fatalf("movie short lost optional request ID correction: %+v", projection)
			}
			if test.review {
				if projection.Readiness != api.ReadinessStatusBlocked || projection.DupeReady || projection.UploadReady ||
					len(projection.RequiredActions) != 1 || projection.RequiredActions[0].Kind != api.RequiredActionAnswerQuestionnaire {
					t.Fatalf("invalid optional request ID lost corrective action: %+v", projection)
				}
			} else if !projection.DupeReady || !projection.UploadReady || len(projection.RequiredActions) != 0 {
				t.Fatalf("empty or valid optional request ID did not recover: %+v", projection)
			}
		})
	}
}
