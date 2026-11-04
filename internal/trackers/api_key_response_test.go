// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBannedGroupRefreshCapturesAPIKeyExpiryBeforeSubmission(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("missing expected synthetic authentication")
		}
		w.Header().Set("X-Api-Key-Expires-At", "2026-11-20T12:00:00Z")
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
	checker := NewBannedGroupCheckerWithRegistry(t.TempDir(), registry)
	checker.client = server.Client()
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"EXAMPLE": {APIKey: "synthetic-key"},
	}}}
	if err := checker.RefreshDynamic(t.Context(), cfg, []string{"EXAMPLE"}, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(guidance.Guidance("synthetic-key", time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)), "2026-11-20") {
		t.Fatal("preflight's authenticated banned-group response lost expiry header")
	}
}
