// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFLDDupeSearchSkips(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		apiKey       string
		tmdbID       int
		category     api.CanonicalCategory
		expectedCode string
		expectedMsg  string
	}{
		{
			name:         "Missing API Key",
			apiKey:       "",
			tmdbID:       12345,
			category:     api.CanonicalCategoryMovie,
			expectedCode: dupe.NotRunMissingCredentials,
			expectedMsg:  "missing api_key for tracker FLD",
		},
		{
			name:         "Missing TMDb ID",
			apiKey:       "key123",
			tmdbID:       0,
			category:     api.CanonicalCategoryMovie,
			expectedCode: dupe.NotRunMissingMetadata,
			expectedMsg:  "missing tmdb id for FLD dupe search",
		},
		{
			name:         "Negative TMDb ID",
			apiKey:       "key123",
			tmdbID:       -1,
			category:     api.CanonicalCategoryMovie,
			expectedCode: dupe.NotRunMissingMetadata,
			expectedMsg:  "missing tmdb id for FLD dupe search",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Config{
				Trackers: config.TrackersConfig{
					Trackers: map[string]config.TrackerConfig{
						"FLD": {APIKey: tc.apiKey},
					},
				},
			}
			searcher := &dupeSearcher{
				cfg:      cfg,
				http:     http.DefaultClient,
				endpoint: "https://flood.st/api/torrents",
			}
			meta := api.DuplicateSubject{
				Identity: api.ExternalIdentity{
					TMDBID:   tc.tmdbID,
					Category: tc.category,
				},
			}
			result := searcher.Search(context.Background(), meta)
			if result.Disposition() != dupe.DispositionNotRun {
				t.Fatalf("expected DispositionNotRun, got %v", result.Disposition())
			}
			if result.Code() != tc.expectedCode {
				t.Fatalf("expected code %q, got %q", tc.expectedCode, result.Code())
			}
			if !strings.Contains(result.SafeMessage(), tc.expectedMsg) {
				t.Fatalf("expected safe message containing %q, got %q", tc.expectedMsg, result.SafeMessage())
			}
		})
	}
}

func TestFLDDupeSearchMovie(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer key123" {
			t.Errorf("unexpected authorization header")
		}
		if id := r.URL.Query().Get("tmdb_id"); id != "movie/12345" {
			t.Errorf("expected tmdb_id movie/12345, got %q", id)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"items": [
				{
					"id": 1,
					"name": "Movie.2026.1080p.BluRay-GRP",
					"media_type": "movie",
					"main_url": "https://flood.st/torrents/1",
					"download_url": "https://flood.st/torrents/1/download",
					"size": 4500000000,
					"files": [
						{"name": "Movie.2026.1080p.BluRay-GRP.mkv"}
					]
				}
			]
		}`)
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"FLD": {APIKey: "key123"},
			},
		},
	}
	searcher := &dupeSearcher{
		cfg:      cfg,
		http:     server.Client(),
		endpoint: server.URL,
	}

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{
			TMDBID:   12345,
			Category: api.CanonicalCategoryMovie,
		},
	}

	result := searcher.Search(context.Background(), meta)
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("expected DispositionResolved, got %v (code: %s, cause: %v)", result.Disposition(), result.Code(), result.Cause())
	}
	entries := result.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Name != "Movie.2026.1080p.BluRay-GRP" {
		t.Errorf("expected name Movie.2026.1080p.BluRay-GRP, got %q", entry.Name)
	}
	if entry.ID != "1" {
		t.Errorf("expected ID 1, got %q", entry.ID)
	}
	if entry.Link != "https://flood.st/torrents/1" {
		t.Errorf("expected link https://flood.st/torrents/1, got %q", entry.Link)
	}
	if !entry.SizeKnown || entry.SizeBytes != 4500000000 {
		t.Errorf("expected size 4500000000, got %d", entry.SizeBytes)
	}
	if entry.FileCount != 1 || len(entry.Files) != 1 || entry.Files[0] != "Movie.2026.1080p.BluRay-GRP.mkv" {
		t.Errorf("expected files, got %#v", entry.Files)
	}
	if entry.Attributes["download_url"] != "https://flood.st/torrents/1/download" {
		t.Errorf("expected download_url attribute, got %q", entry.Attributes["download_url"])
	}
	search := result.SearchEvidence()
	if !search.Complete || search.WorkScope != dupe.WorkScopeProviderID {
		t.Errorf("expected complete search evidence with provider ID, got %#v", search)
	}
}

func TestFLDDupeSearchTV(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if id := q.Get("tmdb_id"); id != "tv/54321" {
			t.Errorf("expected tmdb_id tv/54321, got %q", id)
		}
		if s := q.Get("show_season_number"); s != "2" {
			t.Errorf("expected season 2, got %q", s)
		}
		if e := q.Get("show_episode_number"); e != "3" {
			t.Errorf("expected episode 3, got %q", e)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items": []}`)
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"FLD": {APIKey: "key123"},
			},
		},
	}
	searcher := &dupeSearcher{
		cfg:      cfg,
		http:     server.Client(),
		endpoint: server.URL,
	}

	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{
			TMDBID:   54321,
			Category: api.CanonicalCategoryTV,
		},
		SeasonInt:  2,
		EpisodeInt: 3,
	}

	result := searcher.Search(context.Background(), meta)
	if result.Disposition() != dupe.DispositionResolved {
		t.Fatalf("expected DispositionResolved, got %v", result.Disposition())
	}
	if len(result.Entries()) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(result.Entries()))
	}
}

func TestFLDDupeSearchFailures(t *testing.T) {
	t.Parallel()

	// Status failure (500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := config.Config{
		Trackers: config.TrackersConfig{
			Trackers: map[string]config.TrackerConfig{
				"FLD": {APIKey: "key123"},
			},
		},
	}
	searcher := &dupeSearcher{
		cfg:      cfg,
		http:     server.Client(),
		endpoint: server.URL,
	}
	meta := api.DuplicateSubject{
		Identity: api.ExternalIdentity{
			TMDBID:   12345,
			Category: api.CanonicalCategoryMovie,
		},
	}

	result := searcher.Search(context.Background(), meta)
	if result.Disposition() != dupe.DispositionFailed {
		t.Fatalf("expected DispositionFailed on 500, got %v", result.Disposition())
	}
	if result.Code() != dupe.FailureResponseStatus {
		t.Fatalf("expected code FailureResponseStatus, got %q", result.Code())
	}

	// Bounded response failure (response larger than max)
	serverLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write more than 4MB
		largeChunk := make([]byte, 1024*1024)
		for range 5 {
			_, _ = w.Write(largeChunk)
		}
	}))
	defer serverLarge.Close()

	searcherLarge := &dupeSearcher{
		cfg:      cfg,
		http:     serverLarge.Client(),
		endpoint: serverLarge.URL,
	}
	resultLarge := searcherLarge.Search(context.Background(), meta)
	if resultLarge.Disposition() != dupe.DispositionFailed {
		t.Fatalf("expected DispositionFailed on huge response, got %v", resultLarge.Disposition())
	}
	if resultLarge.Code() != dupe.FailureResponseParse {
		t.Fatalf("expected code FailureResponseParse, got %q", resultLarge.Code())
	}

	// Request failure
	searcherBadReq := &dupeSearcher{
		cfg:      cfg,
		http:     http.DefaultClient,
		endpoint: "://invalid-url",
	}
	resultBadReq := searcherBadReq.Search(context.Background(), meta)
	if resultBadReq.Disposition() != dupe.DispositionFailed {
		t.Fatalf("expected DispositionFailed on invalid URL, got %v", resultBadReq.Disposition())
	}
	if resultBadReq.Code() != dupe.FailureRequest {
		t.Fatalf("expected code FailureRequest, got %q", resultBadReq.Code())
	}
}
