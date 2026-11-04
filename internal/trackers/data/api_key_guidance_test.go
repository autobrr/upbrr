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

func TestAuthenticatedLookupsCaptureAPIKeyExpiry(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("missing expected synthetic authentication")
		}
		requests.Add(1)
		w.Header().Set("X-Api-Key-Expires-At", "2026-11-20T12:00:00Z")
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
	if _, err := client.TorrentInfo(t.Context(), "LST", "1", "", true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile.Site.APIKeyGuidance.Guidance("synthetic-key", now), "2026-11-20") {
		t.Fatal("metadata lookup lost expiry header")
	}
	profile.Site.APIKeyGuidance.Observe("synthetic-key", nil, http.StatusOK)
	if _, _, err := client.SearchTorrents(t.Context(), "LST", url.Values{"tmdbId": {"42"}}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile.Site.APIKeyGuidance.Guidance("synthetic-key", now), "2026-11-20") || requests.Load() < 2 {
		t.Fatal("duplicate lookup lost expiry header")
	}
}
