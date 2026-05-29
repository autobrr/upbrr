// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvmaze

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSearchAutoSelect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/lookup/shows", func(w http.ResponseWriter, r *http.Request) {
		query, _ := url.ParseQuery(r.URL.RawQuery)
		if query.Get("thetvdb") == "999" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":        55,
				"name":      "Example Show",
				"premiered": "2021-01-01",
				"externals": map[string]any{"thetvdb": 999, "imdb": "tt000055"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/search/shows", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"show": map[string]any{
				"id":        55,
				"name":      "Example Show",
				"premiered": "2021-01-01",
				"externals": map[string]any{"thetvdb": 999, "imdb": "tt000055"},
			},
		}})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	result, err := client.Search(context.Background(), SearchInput{
		Filename: "Example Show",
		TVDBID:   "999",
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.SelectedID != 55 {
		t.Fatalf("expected selected ID 55, got %d", result.SelectedID)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.TVDBID != 999 {
		t.Fatalf("expected TVDBID 999, got %d", result.TVDBID)
	}
}

func TestEpisodeFallbackByDate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/shows/55/episodebynumber", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/shows/55/episodesbydate", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":    "Pilot",
			"summary": "<p>Episode summary</p>",
			"season":  1,
			"number":  1,
			"airdate": "2020-01-01",
			"runtime": 50,
			"image":   map[string]any{"original": "ep.jpg", "medium": "epm.jpg"},
			"_links": map[string]any{
				"show": map[string]any{"href": muxURL(r, "/shows/55"), "name": "Example Show"},
			},
		}})
	})
	mux.HandleFunc("/shows/55", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":    "Example Show",
			"summary": "<p>Series summary</p>",
			"image":   map[string]any{"original": "show.jpg", "medium": "showm.jpg"},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	ctx := EpisodeLookupContext{ManualDate: "2020-01-01"}
	data, err := client.GetEpisodeByNumber(context.Background(), 55, 1, 1, ctx)
	if err != nil {
		t.Fatalf("episode lookup failed: %v", err)
	}
	if data.EpisodeName != "Pilot" {
		t.Fatalf("expected Pilot, got %q", data.EpisodeName)
	}
	if data.SeriesName != "Example Show" {
		t.Fatalf("expected series name, got %q", data.SeriesName)
	}
	if data.Overview != "Episode summary" {
		t.Fatalf("expected cleaned overview, got %q", data.Overview)
	}
	if data.SeriesOverview != "Series summary" {
		t.Fatalf("expected cleaned series overview, got %q", data.SeriesOverview)
	}
}

func TestSearchManualIDLoadsExternalIDs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/shows/55", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":        55,
			"name":      "Example Show",
			"premiered": "2021-01-01",
			"externals": map[string]any{"thetvdb": 999, "imdb": "tt1234567"},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	result, err := client.Search(context.Background(), SearchInput{ManualID: 55})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.SelectedID != 55 {
		t.Fatalf("expected selected ID 55, got %d", result.SelectedID)
	}
	if result.IMDBID != 1234567 {
		t.Fatalf("expected imdb id 1234567, got %d", result.IMDBID)
	}
	if result.TVDBID != 999 {
		t.Fatalf("expected tvdb id 999, got %d", result.TVDBID)
	}
}

func TestSearchStrictIDOnlySkipsNameFallback(t *testing.T) {
	searchCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/lookup/shows", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/search/shows", func(w http.ResponseWriter, _ *http.Request) {
		searchCalls++
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.Client(), api.NopLogger{})
	client.baseURL = server.URL

	result, err := client.Search(context.Background(), SearchInput{
		Filename:     "Example Show",
		TVDBID:       "999",
		StrictIDOnly: true,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if result.SelectedID != 0 {
		t.Fatalf("expected no selected id in strict mode, got %d", result.SelectedID)
	}
	if searchCalls != 0 {
		t.Fatalf("expected strict id mode to skip name fallback, got %d search calls", searchCalls)
	}
}

func muxURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}
