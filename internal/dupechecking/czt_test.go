// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCZTHandlerSearchUsesPasskeyAndParsesArray(t *testing.T) {
	t.Parallel()

	const title = "Movie & Show/2024"

	tests := []struct {
		name string
		cfg  config.TrackerConfig
		meta api.PreparedMetadata
	}{
		{
			name: "passkey only",
			cfg:  config.TrackerConfig{Passkey: "passkey123"},
			meta: api.PreparedMetadata{Release: api.ReleaseInfo{Title: title}},
		},
		{
			name: "both credentials use passkey",
			cfg:  config.TrackerConfig{APIKey: "bearer-token", Passkey: "passkey123"},
			meta: api.PreparedMetadata{Release: api.ReleaseInfo{Title: title}},
		},
		{
			name: "padded passkey and title fallback",
			cfg:  config.TrackerConfig{Passkey: " passkey123 "},
			meta: api.PreparedMetadata{ReleaseName: title},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api.php" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if got := r.URL.Query().Get("action"); got != "search-torrents" {
					t.Fatalf("action query: got %q", got)
				}
				if got := r.URL.Query().Get("type"); got != "name" {
					t.Fatalf("type query: got %q", got)
				}
				if got := r.URL.Query().Get("passkey"); got != "passkey123" {
					t.Fatalf("passkey query: got %q", got)
				}
				if got := r.URL.Query().Get("query"); got != title {
					t.Fatalf("query: got %q", got)
				}
				if got := r.URL.Query().Get("incldead"); got != "1" {
					t.Fatalf("incldead query: got %q", got)
				}
				if got := r.Header.Get("Authorization"); got != "" {
					t.Fatalf("unexpected authorization header")
				}
				_, _ = io.WriteString(w, `[{"id":"77","name":"Movie.2024.1080p.WEB-DL-GRP","url":"https://czteam.me/details.php?id=77","size":12345}]`)
			}))
			defer server.Close()

			tc.cfg.URL = server.URL
			handler := cztHandler{
				cfg:    cztTestConfig(tc.cfg),
				http:   server.Client(),
				logger: api.NopLogger{},
			}
			entries, notes, err := handler.Search(context.Background(), tc.meta, "CZT")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(notes) != 0 {
				t.Fatalf("unexpected notes: %v", notes)
			}
			if len(entries) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(entries))
			}
			entry := entries[0]
			if entry.ID != "77" || entry.Name != "Movie.2024.1080p.WEB-DL-GRP" || entry.Link == "" {
				t.Fatalf("unexpected entry: %#v", entry)
			}
			if !entry.SizeKnown || entry.SizeBytes != 12345 {
				t.Fatalf("unexpected size fields: %#v", entry)
			}
		})
	}
}

func TestCZTHandlerSearchCredentialMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       config.TrackerConfig
		wantSkip  string
		wantError string
	}{
		{
			name:     "missing all credentials skips",
			cfg:      config.TrackerConfig{},
			wantSkip: "missing passkey",
		},
		{
			name:     "api key only skips",
			cfg:      config.TrackerConfig{APIKey: "bearer-token"},
			wantSkip: "requires passkey",
		},
		{
			name:     "padded api key only skips",
			cfg:      config.TrackerConfig{APIKey: " bearer-token "},
			wantSkip: "requires passkey",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := cztHandler{
				cfg:    cztTestConfig(tc.cfg),
				http:   http.DefaultClient,
				logger: api.NopLogger{},
			}
			_, notes, err := handler.Search(context.Background(), api.PreparedMetadata{
				Release: api.ReleaseInfo{Title: "Movie"},
			}, "CZT")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
				}
				if len(notes) != 0 {
					t.Fatalf("expected no skip notes on failure, got %v", notes)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(notes) != 1 || !strings.Contains(notes[0], tc.wantSkip) {
				t.Fatalf("expected skip note containing %q, got %v", tc.wantSkip, notes)
			}
		})
	}
}

func TestCZTHandlerSearchRemoteFailuresReturnErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantError  string
	}{
		{name: "non 2xx", statusCode: http.StatusUnauthorized, body: `{"error":"bad auth"}`, wantError: "HTTP status 401"},
		{name: "auth object", statusCode: http.StatusOK, body: `{"error":"bad auth"}`, wantError: "unexpected response shape"},
		{name: "null", statusCode: http.StatusOK, body: `null`, wantError: "empty response"},
		{name: "scalar", statusCode: http.StatusOK, body: `"ok"`, wantError: "unexpected response shape"},
		{name: "malformed", statusCode: http.StatusOK, body: `{`, wantError: "decode JSON GET response"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			handler := cztHandler{
				cfg:    cztTestConfig(config.TrackerConfig{URL: server.URL, Passkey: "passkey123"}),
				http:   server.Client(),
				logger: api.NopLogger{},
			}
			_, notes, err := handler.Search(context.Background(), api.PreparedMetadata{
				Release: api.ReleaseInfo{Title: "Movie"},
			}, "CZT")
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
			}
			if len(notes) != 0 {
				t.Fatalf("expected no skip notes on failure, got %v", notes)
			}
			if strings.Contains(err.Error(), "passkey123") {
				t.Fatalf("error leaked passkey: %v", err)
			}
		})
	}
}

func TestCZTHandlerSearchMissingTitleSkipsWithoutHTTP(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called.Store(true)
	}))
	defer server.Close()

	handler := cztHandler{
		cfg:    cztTestConfig(config.TrackerConfig{URL: server.URL, Passkey: "passkey123"}),
		http:   server.Client(),
		logger: api.NopLogger{},
	}
	_, notes, err := handler.Search(context.Background(), api.PreparedMetadata{}, "CZT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "missing title") {
		t.Fatalf("expected missing title skip, got %v", notes)
	}
	if called.Load() {
		t.Fatalf("expected no HTTP call")
	}
}

func TestCZTServiceMarksAPIKeyOnlySearchSkipped(t *testing.T) {
	t.Parallel()

	svc := NewService(cztTestConfig(config.TrackerConfig{APIKey: "bearer-token"}), api.NopLogger{})
	summary, err := svc.Check(context.Background(), api.PreparedMetadata{
		SourcePath: "source.mkv",
		Release:    api.ReleaseInfo{Title: "Movie"},
	}, []string{"CZT"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	result := summary.Results[0]
	if result.Status != "skipped" || !result.Skipped {
		t.Fatalf("expected skipped result, got %#v", result)
	}
	if result.Error != "" {
		t.Fatalf("expected no error, got %q", result.Error)
	}
	if !strings.Contains(result.SkipReason, "requires passkey") {
		t.Fatalf("expected passkey skip, got %q", result.SkipReason)
	}
	if strings.Contains(result.SkipReason, "bearer-token") || strings.Contains(strings.Join(result.Notes, " "), "bearer-token") {
		t.Fatalf("skip result leaked API key: %#v", result)
	}
}

func cztTestConfig(tracker config.TrackerConfig) config.Config {
	return config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"CZT": tracker,
	}}}
}
