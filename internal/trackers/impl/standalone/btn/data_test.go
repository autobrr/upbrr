// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestDataLookup(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/btn" {
			http.NotFound(w, r)
			return
		}
		var rpc struct {
			Method string `json:"method"`
			Params struct {
				Search map[string]string `json:"search"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil || rpc.Method != "getTorrents" || rpc.Params.Search["id"] != "42" {
			t.Errorf("unexpected BTN lookup request: %#v err=%v", rpc, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"torrents": map[string]any{"42": map[string]any{"ImdbID": 1234567, "TvdbID": 76543}}}})
	}))
	defer server.Close()
	token := strings.Repeat("a", 30)
	lookup, ok := New().NewDataLookup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BTN": {APIKey: token}}}}, server.Client(), nil).(*dataLookup)
	if !ok {
		t.Fatal("expected BTN data lookup")
	}
	lookup.endpoint = server.URL + "/btn"
	result, err := lookup.Lookup(context.Background(), trackers.DataLookupRequest{TrackerID: "42", OnlyID: true})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result.IMDBID != 1234567 || result.TVDBID != 76543 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDataLookupRejectsUnboundTorrents(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		rows map[string]any
	}{
		{name: "wrong map key", rows: map[string]any{"1": map[string]any{"ImdbID": 1234567}}},
		{name: "wrong torrent ID", rows: map[string]any{"42": map[string]any{"TorrentID": "1", "ImdbID": 1234567}}},
		{name: "multiple rows", rows: map[string]any{"42": map[string]any{"ImdbID": 1234567}, "43": map[string]any{"ImdbID": 2345678}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"torrents": tt.rows}})
			}))
			defer server.Close()
			lookup := &dataLookup{
				cfg:      configWithBTNAPIKey(),
				http:     server.Client(),
				endpoint: server.URL,
			}
			result, err := lookup.Lookup(t.Context(), trackers.DataLookupRequest{TrackerID: "42", OnlyID: true})
			if err == nil || result.TrackerID != "" || result.IMDBID != 0 || result.TVDBID != 0 {
				t.Fatalf("unbound response populated identifiers: result=%+v err=%v", result, err)
			}
		})
	}
}
