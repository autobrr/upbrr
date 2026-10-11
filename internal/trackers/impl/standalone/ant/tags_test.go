// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestGenreTagResolutionAndQuestionnaire(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		tmdb       *api.TMDBMetadata
		imdb       string
		genres     []string
		provenance api.FactProvenance
		answers    map[string]string
		want       string
		question   bool
		required   bool
	}{
		{
			name:     "missing genres",
			question: true,
			required: true,
		},
		{
			name: "single genre",
			tmdb: &api.TMDBMetadata{Genres: "Action"},
			want: "action",
		},
		{
			name: "single TMDB alias",
			tmdb: &api.TMDBMetadata{Genres: "Science Fiction"},
			want: "sci.fi",
		},
		{
			name: "single IMDb alias",
			imdb: "Sci-Fi",
			want: "sci.fi",
		},
		{
			name: "comma and semicolon whitespace",
			tmdb: &api.TMDBMetadata{Genres: "  Action ; Adventure, Thriller  "},
			want: "action,adventure,thriller",
		},
		{
			name: "reported combination",
			tmdb: &api.TMDBMetadata{Genres: "Science Fiction, Action, Adventure, Thriller"},
			want: "sci.fi,action,adventure,thriller",
		},
		{
			name: "provider aliases deduplicate",
			tmdb: &api.TMDBMetadata{Genres: "Science Fiction, Sci-Fi, sci.fi"},
			want: "sci.fi",
		},
		{
			name: "TMDB precedes IMDb",
			tmdb: &api.TMDBMetadata{Genres: "Drama"},
			imdb: "Action",
			want: "drama",
		},
		{
			name: "missing TMDB",
			imdb: "Sci-Fi, Action",
			want: "sci.fi,action",
		},
		{
			name: "empty TMDB",
			tmdb: &api.TMDBMetadata{},
			imdb: "Drama",
			want: "drama",
		},
		{
			name: "unsupported TMDB",
			tmdb: &api.TMDBMetadata{Genres: "Unknown"},
			imdb: "Drama",
			want: "drama",
		},
		{
			name:     "no usable provider genres",
			tmdb:     &api.TMDBMetadata{Genres: "Unknown"},
			imdb:     "Unknown",
			question: true,
			required: true,
		},
		{
			name:       "manual genres",
			tmdb:       &api.TMDBMetadata{Genres: "Action"},
			imdb:       "Drama",
			genres:     []string{"Science Fiction"},
			provenance: api.FactProvenanceManual,
			want:       "sci.fi",
		},
		{
			name:       "manual genres clear with TMDB",
			tmdb:       &api.TMDBMetadata{Genres: "Science Fiction, Action, Adventure, Thriller"},
			imdb:       "Drama",
			provenance: api.FactProvenanceManualEmpty,
			question:   true,
		},
		{
			name:       "manual genres clear without TMDB",
			imdb:       "Drama",
			provenance: api.FactProvenanceManualEmpty,
			question:   true,
			required:   true,
		},
		{
			name:       "explicit tags override genres",
			tmdb:       &api.TMDBMetadata{Genres: "Action"},
			genres:     []string{"Drama"},
			provenance: api.FactProvenanceManual,
			answers:    map[string]string{"tags": "Custom Tag, Custom Tag"},
			want:       "custom.tag",
			question:   true,
		},
		{
			name:     "separator-only answer stays normalized",
			tmdb:     &api.TMDBMetadata{Genres: "Action"},
			answers:  map[string]string{"tags": " , ; "},
			question: true,
		},
		{
			name:     "explicit clear with TMDB",
			tmdb:     &api.TMDBMetadata{Genres: "Action"},
			imdb:     "Drama",
			answers:  map[string]string{"tags": ""},
			question: true,
		},
		{
			name:     "explicit clear without TMDB",
			imdb:     "Drama",
			answers:  map[string]string{"tags": " "},
			question: true,
			required: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			mediaInfoPath := filepath.Join(tmp, "MEDIAINFO.txt")
			torrentPath := filepath.Join(tmp, "Example.torrent")
			for path, content := range map[string]string{mediaInfoPath: "General\nUnique ID : 123", torrentPath: "dummy"} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.tmdb != nil {
				test.tmdb.TMDBID = 123
				test.tmdb.Title = "Example Movie"
				test.tmdb.Year = 2026
			}
			meta := api.UploadSubject{
				ReleaseName: "Example.Movie.2026.1080p.BluRay.x264-GRP",
				Source:      "BluRay",
				Type:        "ENCODE",
				Release: api.ReleaseInfo{
					Title:      "Example Movie",
					Year:       2026,
					Category:   "MOVIE",
					Resolution: "1080p",
				},
				SourcePath:                  filepath.Join(tmp, "Example.mkv"),
				TorrentPath:                 torrentPath,
				MediaInfoTextPath:           mediaInfoPath,
				Identity:                    api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
				ProviderMetadata:            api.SourceScopedMetadata{TMDB: test.tmdb, IMDB: &api.IMDBMetadata{Genres: test.imdb}},
				EffectiveMetadata:           api.EffectiveMetadata{Genres: test.genres, GenresProvenance: test.provenance},
				TrackerQuestionnaireAnswers: map[string]map[string]string{"ANT": test.answers},
			}
			input := trackers.PreparationInput{
				Tracker:       "ANT",
				Meta:          meta,
				TrackerConfig: config.TrackerConfig{APIKey: "token"},
				Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{}),
				Logger:        api.NopLogger{},
			}
			projected := projectionQuestionnaire(input)
			projection, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			blocked := test.required && test.want == ""
			hasQuestionAction := slices.ContainsFunc(projection.RequiredActions, func(action api.RequiredAction) bool {
				return action.Kind == api.RequiredActionAnswerQuestionnaire
			})
			// Missing fetched TMDB metadata remains an independent eligibility block.
			expectReady := !blocked && test.tmdb != nil
			if hasQuestionAction != blocked || projection.DupeReady != expectReady || projection.UploadReady != expectReady {
				t.Fatalf("questionnaire readiness disagrees with payload: %+v", projection)
			}
			entry, err := New().prepareDryRun(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if got := entry.Payload["tags"]; got != test.want {
				t.Fatalf("payload tags = %q, want %q", got, test.want)
			}
			if test.want == "" {
				if _, present := entry.Payload["tags"]; present {
					t.Fatal("empty tags must be omitted from the payload")
				}
			}
			if !reflect.DeepEqual(projected, entry.Questionnaire) {
				t.Fatalf("projection and dry-run questionnaires differ: %#v / %#v", projected, entry.Questionnaire)
			}
			if !test.question {
				if projected == nil || len(projected.Fields) != 1 || projected.Fields[0].Key != "requestid" || projected.Fields[0].Required {
					t.Fatalf("usable genres should need no question: %#v", projected)
				}
				return
			}
			if projected == nil || len(projected.Fields) != 2 {
				t.Fatalf("expected one tags field, got %#v", projected)
			}
			field := projected.Fields[0]
			if field.Key != "tags" || field.Value != test.want || field.Required != test.required {
				t.Fatalf("tags field = %#v, want value %q and required %t", field, test.want, test.required)
			}
		})
	}
}
