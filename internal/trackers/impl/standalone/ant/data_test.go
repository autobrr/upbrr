// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestDataLookupSendsAPIKeyHeader(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "" || r.URL.Query().Get("t") != "search" || r.URL.Query().Get("filename") != "Example.Release.mkv" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-Api-Key") != "token" || r.Header.Get("User-Agent") == "" {
			t.Error("unexpected request headers")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"item": []map[string]any{{"imdb": "tt1234567", "tmdb": 765}}})
	}))
	defer server.Close()

	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "token"}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected ANT data lookup")
	}
	lookup.endpoint = server.URL
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{SearchName: "Example.Release.mkv"})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.IMDBID != 1234567 || result.TMDBID != 765 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
