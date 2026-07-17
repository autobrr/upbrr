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
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"torrents": map[string]any{"1": map[string]any{"ImdbID": 1234567, "TvdbID": 76543}}}})
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
