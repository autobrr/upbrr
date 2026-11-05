// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAuthenticatedLookupsDoNotChangeUploadExpiry(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	var reportHeader atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("missing expected synthetic authentication")
		}
		requests.Add(1)
		if reportHeader.Load() {
			w.Header().Set("X-Api-Key-Expires-At", "2040-01-01T00:00:00Z")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1,"total":0,"per_page":100}}`))
	}))
	defer server.Close()
	profile := lst.Profile()
	profile.BaseURL = server.URL
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(profile)); err != nil {
		t.Fatal(err)
	}
	client := data.NewClientWithRegistry(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"LST": {APIKey: "synthetic-key"},
	}}}, api.NopLogger{}, server.Client(), registry)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, submitted := range []bool{false, true} {
		if submitted {
			header := make(http.Header)
			header.Set(profile.Site.APIKeyGuidance.Header, "2026-11-20T12:00:00Z")
			profile.Site.APIKeyGuidance.Observe("synthetic-key", header, http.StatusOK)
		}
		for _, present := range []bool{true, false} {
			reportHeader.Store(present)
			if _, err := client.TorrentInfo(t.Context(), "LST", "1", "", true, false); err != nil {
				t.Fatal(err)
			}
			if _, _, err := client.SearchTorrents(t.Context(), "LST", url.Values{"tmdbId": {"42"}}, false); err != nil {
				t.Fatal(err)
			}
			got := profile.Site.APIKeyGuidance.Guidance("synthetic-key", now)
			if !submitted && got != "" || submitted && !strings.Contains(got, "2026-11-20") {
				t.Fatal("authenticated GET changed upload POST guidance")
			}
		}
	}
	if requests.Load() != 8 {
		t.Fatal("lookup isolation was not exercised for both header states")
	}
}
