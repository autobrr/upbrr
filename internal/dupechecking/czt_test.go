// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"context"
	"errors"
	"fmt"
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

			handlerErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := assertCZTSearchRequest(r, title); err != nil {
					select {
					case handlerErr <- err:
					default:
					}
					w.WriteHeader(http.StatusInternalServerError)
					return
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
			select {
			case err := <-handlerErr:
				t.Fatalf("handler: %v", err)
			default:
			}
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

func TestCZTHandlerSearchNormalizesBaseURLPathAndQuery(t *testing.T) {
	t.Parallel()

	const title = "Movie"

	handlerErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := assertCZTSearchRequest(r, title); err != nil {
			select {
			case handlerErr <- err:
			default:
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `[{"id":"77","name":"Movie","url":"https://czteam.me/details.php?id=77"}]`)
	}))
	defer server.Close()

	tests := []struct {
		name string
		url  string
	}{
		{name: "path and query", url: server.URL + "/nested/path?token=ignored"},
		{name: "username only", url: strings.Replace(server.URL, "://", "://username@", 1) + "/nested/path?token=ignored"},
		{name: "username password", url: strings.Replace(server.URL, "://", "://username:password@", 1) + "/nested/path?token=ignored"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := cztHandler{
				cfg:    cztTestConfig(config.TrackerConfig{URL: tc.url, Passkey: "passkey123"}),
				http:   server.Client(),
				logger: api.NopLogger{},
			}
			entries, notes, err := handler.Search(context.Background(), api.PreparedMetadata{
				Release: api.ReleaseInfo{Title: title},
			}, "CZT")
			select {
			case err := <-handlerErr:
				t.Fatalf("handler: %v", err)
			default:
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(notes) != 0 || len(entries) != 1 {
				t.Fatalf("expected one entry and no notes, got entries=%v notes=%v", entries, notes)
			}
		})
	}
}

func TestNormalizeCZTSearchBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "origin path query fragment", value: " https://czteam.me/nested/path?token=ignored#frag ", want: "https://czteam.me"},
		{name: "username only", value: "https://username@czteam.me/nested/path?token=ignored", want: "https://czteam.me"},
		{name: "username password", value: "https://username:password@czteam.me/nested/path?token=ignored", want: "https://czteam.me"},
		{name: "schemeless fallback", value: "czteam.me/nested/path/", want: "czteam.me/nested/path"},
		{name: "malformed fallback", value: "://czteam.me/nested/path/", want: "://czteam.me/nested/path"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCZTSearchBaseURL(tc.value); got != tc.want {
				t.Fatalf("normalizeCZTSearchBaseURL(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestCZTHandlerSearchCancellationAfterResponseReturnsNoEntries(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{Transport: cztRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`[{"id":"77","name":"Movie","url":"https://czteam.me/details.php?id=77"}]`)),
			Request:    req,
		}, nil
	})}
	handler := cztHandler{
		cfg:    cztTestConfig(config.TrackerConfig{URL: "https://czteam.me", Passkey: "passkey123"}),
		http:   client,
		logger: api.NopLogger{},
	}

	entries, notes, err := handler.Search(ctx, api.PreparedMetadata{
		Release: api.ReleaseInfo{Title: "Movie"},
	}, "CZT")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if len(entries) != 0 || len(notes) != 0 {
		t.Fatalf("expected no entries or notes after cancellation, got entries=%v notes=%v", entries, notes)
	}
}

type cztRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cztRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCZTHandlerSearchRequestAssertionReportsOnTestGoroutine(t *testing.T) {
	t.Parallel()

	const title = "Movie & Show/2024"

	tests := []struct {
		name        string
		target      func(baseURL string) string
		wantErrPart string
	}{
		{
			name: "wrong path",
			target: func(baseURL string) string {
				return baseURL + "/wrong"
			},
			wantErrPart: "unexpected path",
		},
		{
			name: "wrong query",
			target: func(baseURL string) string {
				return baseURL + "/api.php?action=search-torrents&type=name&passkey=passkey123&query=Wrong&incldead=1"
			},
			wantErrPart: "query",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handlerErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := assertCZTSearchRequest(r, title); err != nil {
					select {
					case handlerErr <- err:
					default:
					}
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.target(server.URL), nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, resp.StatusCode)
			}

			select {
			case err := <-handlerErr:
				if !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf("expected handler assertion error containing %q, got %v", tc.wantErrPart, err)
				}
			default:
				t.Fatalf("expected handler assertion error")
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
		wantCode  string
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
			wantCode: api.DupeSkipCodeCZTAPIKeyOnly,
		},
		{
			name:     "padded api key only skips",
			cfg:      config.TrackerConfig{APIKey: " bearer-token "},
			wantSkip: "requires passkey",
			wantCode: api.DupeSkipCodeCZTAPIKeyOnly,
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
			code, displayNotes := splitSkipCodeNotes(notes)
			if code != tc.wantCode {
				t.Fatalf("expected skip code %q, got %q from notes %v", tc.wantCode, code, notes)
			}
			if len(displayNotes) != 1 || !strings.Contains(displayNotes[0], tc.wantSkip) {
				t.Fatalf("expected display skip note containing %q, got %v", tc.wantSkip, displayNotes)
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
	if result.SkipCode != api.DupeSkipCodeCZTAPIKeyOnly {
		t.Fatalf("expected structured skip code, got %q", result.SkipCode)
	}
	if len(result.SkipRules) != 0 {
		t.Fatalf("expected api-key-only skip not to use rule grouping keys, got %#v", result.SkipRules)
	}
	if !strings.Contains(result.SkipReason, "requires passkey") {
		t.Fatalf("expected passkey skip, got %q", result.SkipReason)
	}
	if strings.Contains(strings.Join(result.Notes, " "), "skip-code:") {
		t.Fatalf("structured skip marker leaked into display notes: %#v", result.Notes)
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

func assertCZTSearchRequest(r *http.Request, title string) error {
	if r.URL.Path != "/api.php" {
		return fmt.Errorf("unexpected path: %s", r.URL.Path)
	}
	if got := r.URL.Query().Get("action"); got != "search-torrents" {
		return fmt.Errorf("action query: got %q", got)
	}
	if got := r.URL.Query().Get("type"); got != "name" {
		return fmt.Errorf("type query: got %q", got)
	}
	if got := r.URL.Query().Get("passkey"); got != "passkey123" {
		return fmt.Errorf("passkey query: got %q", got)
	}
	if got := r.URL.Query().Get("query"); got != title {
		return fmt.Errorf("query: got %q", got)
	}
	if got := r.URL.Query().Get("incldead"); got != "1" {
		return fmt.Errorf("incldead query: got %q", got)
	}
	if got := r.Header.Get("Authorization"); got != "" {
		return errors.New("unexpected authorization header")
	}
	return nil
}
