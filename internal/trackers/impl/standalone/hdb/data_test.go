// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDataLookup(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": []any{map[string]any{
			"id":    "321",
			"hash":  "deadbeef",
			"imdb":  map[string]any{"id": "998877"},
			"tvdb":  map[string]any{"id": "5544"},
			"descr": "Text\n[url=https://imgbox.com/abc][img]https://thumbs2.imgbox.com/abc_t.png[/img][/url]",
		}}})
	}))
	defer server.Close()
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"HDB": {Username: "user", Passkey: "pass"}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected HDB data lookup")
	}
	lookup.endpoint = server.URL
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{TrackerID: "321", KeepImages: true})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.IMDBID != 998877 || result.TVDBID != 5544 || result.InfoHash != "deadbeef" || result.Description != "Text" || len(result.Images) != 1 {
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
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"HDB": {Username: "user", Passkey: "pass"}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected HDB data lookup")
	}
	lookup.endpoint = server.URL
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{Meta: api.UploadSubject{SourcePath: `D:\TV\Example.Show.S04E01.2160p.WEB.h265-GRP.mkv`, FileList: []string{"Example.Show.S04E01.2160p.WEB.h265-GRP.mkv"}}, KeepImages: true})
	if err != nil || result.HasData() || requested {
		t.Fatalf("err=%v data=%t requested=%t", err, result.HasData(), requested)
	}
}
