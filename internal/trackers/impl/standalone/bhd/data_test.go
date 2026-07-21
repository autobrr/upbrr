// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDataLookup(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bhd/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 1,
			"success":     true,
			"results": []any{map[string]any{
				"id":          "99",
				"imdb_id":     "tt1234567",
				"tmdb_id":     "movie/765",
				"description": "hello\n[url=https://pixhost.to/full/example][img]https://pixhost.to/example.png[/img][/url]",
			}},
		})
	}))
	defer server.Close()

	token := strings.Repeat("a", minDataTokenLength)
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: token, BhdRSSKey: token}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected BHD data lookup")
	}
	lookup.baseURL = server.URL + "/bhd"
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{
		Meta:       api.UploadSubject{SourcePath: "/tmp/release"},
		SearchName: "release.mkv",
		KeepImages: true,
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.TrackerID != "99" || result.IMDBID != 1234567 || result.TMDBID != 765 || result.Category != "MOVIE" || result.Description != "hello" || len(result.Images) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDataLookupSkipsUnfilteredSearch(t *testing.T) {
	t.Parallel()
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requested = true
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	token := strings.Repeat("a", minDataTokenLength)
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: token, BhdRSSKey: token}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected BHD data lookup")
	}
	lookup.baseURL = server.URL
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{Meta: api.UploadSubject{SourcePath: `D:\TV\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`, FileList: []string{"Example.Show.S04E01.2160p.WEB.h265-GRP.mkv"}}, KeepImages: true})
	if err != nil || result.HasData() || requested {
		t.Fatalf("err=%v data=%t requested=%t", err, result.HasData(), requested)
	}
}
