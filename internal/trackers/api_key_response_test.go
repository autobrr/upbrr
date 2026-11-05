// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBannedGroupRefreshDoesNotChangeUploadExpiry(t *testing.T) {
	t.Parallel()
	var reportHeader atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("missing expected synthetic authentication")
		}
		if reportHeader.Load() {
			w.Header().Set("X-Api-Key-Expires-At", "2040-01-01T00:00:00Z")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	guidance := &APIKeyGuidance{Header: "X-Api-Key-Expires-At", Message: func(expiry APIKeyExpiry, _ time.Time) string {
		return expiry.ExpiresAt.Format(time.RFC3339)
	}}
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:         "EXAMPLE",
		BaseURL:      server.URL,
		Definition:   stubDefinition{name: "EXAMPLE"},
		AuthPolicy:   &AuthPolicy{APIKeyGuidance: guidance},
		BannedPolicy: &BannedGroupPolicy{EndpointPath: "/api/bannedReleaseGroups", RequireAPIKey: true},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"EXAMPLE": {APIKey: "synthetic-key"},
	}}}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, submitted := range []bool{false, true} {
		if submitted {
			header := make(http.Header)
			header.Set(guidance.Header, "2026-11-20T12:00:00Z")
			guidance.Observe("synthetic-key", header, http.StatusOK)
		}
		for _, present := range []bool{true, false} {
			reportHeader.Store(present)
			checker := NewBannedGroupCheckerWithRegistry(t.TempDir(), registry)
			checker.client = server.Client()
			if err := checker.RefreshDynamic(t.Context(), cfg, []string{"EXAMPLE"}, api.NopLogger{}); err != nil {
				t.Fatal(err)
			}
			got := guidance.Guidance("synthetic-key", now)
			if !submitted && got != "" || submitted && got != "2026-11-20T12:00:00Z" {
				t.Fatal("banned-group GET changed upload POST guidance")
			}
		}
	}
}
