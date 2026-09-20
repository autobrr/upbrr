// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerAuthLoginReportsSessionPersistenceFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/login" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"refreshed-session"}`))
	}))
	t.Cleanup(server.Close)
	cfg := config.TrackerConfig{Username: "user", Password: "pass"}
	for _, tc := range []struct{ name, dbPath string }{
		{name: "directory", dbPath: t.TempDir()},
		{name: "empty"},
		{name: "whitespace", dbPath: " \t "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := resolveSessionForTrackerAuthLoginAt(t.Context(), cfg, tc.dbPath, api.TrackerAuthLoginRequest{}, server.URL)
			if err == nil || !strings.Contains(err.Error(), "save API session") {
				t.Fatalf("tracker-auth persistence error = %v", err)
			}
			for _, logger := range []api.Logger{nil, api.NopLogger{}} {
				token, err := resolveAPIKey(t.Context(), trackers.PreparationInput{
					TrackerConfig: cfg,
					Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: tc.dbPath}}),
					Logger:        logger,
				}, server.URL)
				if err != nil || token != "refreshed-session" {
					t.Fatalf("current upload token=%q err=%v", token, err)
				}
			}
		})
	}
}

func TestRTFAPISessionDoesNotCrossCredentialOrSiteBindings(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	writeRTFWebAuthFixture(t, dbPath)
	configured := config.TrackerConfig{
		APIKey:   "configured-key",
		Username: "user",
		Password: "pass",
	}
	seedRTFConfig(t, dbPath, configured)
	baseURL := "https://retroflix.example"
	if err := persistRefreshedRTFAPIKey(ctx, dbPath, baseURL, configured, "session-token"); err != nil {
		t.Fatalf("persist API session: %v", err)
	}

	for _, tt := range []struct {
		name string
		base string
		cfg  config.TrackerConfig
	}{
		{
			name: "changed api key",
			base: baseURL,
			cfg: config.TrackerConfig{
				APIKey:   "replacement-key",
				Username: "user",
				Password: "pass",
			},
		},
		{
			name: "changed credentials",
			base: baseURL,
			cfg: config.TrackerConfig{
				APIKey:   "configured-key",
				Username: "user",
				Password: "replacement-pass",
			},
		},
		{
			name: "changed site",
			base: "https://other-retroflix.example",
			cfg:  configured,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			token, err := loadCachedRTFAPIKey(ctx, dbPath, tt.base, tt.cfg)
			if err != nil {
				t.Fatalf("load API session: %v", err)
			}
			if token != "" {
				t.Fatal("session token was reused after its configuration binding changed")
			}
		})
	}
}
