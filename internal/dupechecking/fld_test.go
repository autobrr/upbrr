package dupechecking

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFLDHandlerSearchSkips(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		apiKey       string
		tmdbID       int
		category     string
		expectedSkip string
	}{
		{
			name:         "Missing API Key",
			apiKey:       "",
			tmdbID:       12345,
			expectedSkip: "missing api_key for tracker FLD",
		},
		{
			name:         "Missing TMDb ID",
			apiKey:       "key",
			tmdbID:       0,
			expectedSkip: "missing tmdb id for FLD dupe search",
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
			handler := fldHandler{
				cfg:  cfg,
				http: http.DefaultClient,
			}
			meta := api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{
					TMDBID:   tc.tmdbID,
					Category: tc.category,
				},
			}
			entries, notes, err := handler.Search(context.Background(), meta, "FLD")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("expected 0 entries, got %d", len(entries))
			}
			if len(notes) != 1 || !stringsContains(notes[0], tc.expectedSkip) {
				t.Fatalf("expected skip note containing %q, got %v", tc.expectedSkip, notes)
			}
		})
	}
}

func TestFLDHandlerSearchMovie(t *testing.T) {
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
					"id": "1",
					"name": "Movie.2026.1080p.BluRay-GRP",
					"main_url": "https://flood.st/torrents/1",
					"size": 4500000000
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
	handler := fldHandler{
		cfg:     cfg,
		http:    server.Client(),
		baseURL: server.URL,
	}

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{
			TMDBID:   12345,
			Category: "MOVIE",
		},
	}

	entries, notes, err := handler.Search(context.Background(), meta, "FLD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "Movie.2026.1080p.BluRay-GRP" {
		t.Errorf("expected name Movie.2026.1080p.BluRay-GRP, got %q", entries[0].Name)
	}
	if entries[0].ID != "1" {
		t.Errorf("expected ID 1, got %q", entries[0].ID)
	}
	if entries[0].Link != "https://flood.st/torrents/1" {
		t.Errorf("expected link https://flood.st/torrents/1, got %q", entries[0].Link)
	}
	if !entries[0].SizeKnown || entries[0].SizeBytes != 4500000000 {
		t.Errorf("expected size 4500000000, got %d (known: %t)", entries[0].SizeBytes, entries[0].SizeKnown)
	}
}

func TestFLDHandlerSearchTV(t *testing.T) {
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
	handler := fldHandler{
		cfg:     cfg,
		http:    server.Client(),
		baseURL: server.URL,
	}

	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{
			TMDBID:   54321,
			Category: "TV",
		},
		SeasonInt:  2,
		EpisodeInt: 3,
	}

	entries, notes, err := handler.Search(context.Background(), meta, "FLD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringsContainsSimple(s, sub))
}

func stringsContainsSimple(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
