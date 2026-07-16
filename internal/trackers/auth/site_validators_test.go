// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidateFFStoredCookiesReadsFullSuccessBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", authPreviewBytes+32) + "friends.php"))
	}))
	defer server.Close()

	err := validateFFStoredCookies(context.Background(), server.URL, []*http.Cookie{{Name: "session", Value: "ok"}})
	if err != nil {
		t.Fatalf("expected marker beyond preview cap to validate session: %v", err)
	}
}

func TestResolveARStoredSessionRequiresAuthenticatedLogoutMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantInvalid bool
	}{
		{
			name: "authenticated logout link",
			body: `<a href="https://alpharatio.cc/logout.php?auth=session-key">Logout</a>`,
		},
		{
			name: "relative logout link",
			body: `<a href="/logout.php?auth=session-key">Logout</a>`,
		},
		{
			name:        "logout link without auth key",
			body:        `<a href="https://alpharatio.cc/logout.php?auth=">Logout</a>`,
			wantInvalid: true,
		},
		{
			name:        "logout link without query",
			body:        `<a href="https://alpharatio.cc/logout.php">Logout</a>`,
			wantInvalid: true,
		},
		{
			name:        "foreign logout link",
			body:        `<a href="https://example.invalid/logout.php?auth=session-key">Logout</a>`,
			wantInvalid: true,
		},
		{
			name:        "protocol-relative foreign logout link",
			body:        `<a href="//example.invalid/logout.php?auth=session-key">Logout</a>`,
			wantInvalid: true,
		},
		{
			name:        "arbitrary help page",
			body:        `<html><h1>Browse help</h1></html>`,
			wantInvalid: true,
		},
		{
			name:        "unrecognized login page",
			body:        `<form action="/login.php"><input name="username"><input name="password"></form>`,
			wantInvalid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dbPath := newTrackerAuthTestDB(t)
			if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "abc"}); err != nil {
				t.Fatalf("SaveTrackerCookieMap: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != arIndexPath {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			err := resolveARSessionForTrackerAuth(ctx, config.TrackerConfig{URL: server.URL}, dbPath, api.TrackerAuthLoginRequest{})
			if !tt.wantInvalid {
				if err != nil {
					t.Fatalf("expected authenticated AR index page to validate: %v", err)
				}
				return
			}
			validationErr, ok := asValidationError(err)
			if !ok {
				t.Fatalf("expected validation error, got %v", err)
			}
			if !validationErr.ConfirmedInvalid || validationErr.Transient {
				t.Fatalf("expected missing AR logout marker to invalidate session, got %+v", validationErr)
			}
		})
	}
}

func TestValidateARStoredCookiesBoundsSuccessfulResponseBody(t *testing.T) {
	t.Parallel()

	const marker = `<a href="/logout.php?auth=session-key">Logout</a>`
	tests := []struct {
		name          string
		bodySize      int
		wantTransient bool
	}{
		{name: "at limit", bodySize: authResponseBytes},
		{
			name:          "over limit",
			bodySize:      authResponseBytes + 1,
			wantTransient: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := marker + strings.Repeat("a", tt.bodySize-len(marker))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)

			err := validateARStoredCookies(context.Background(), server.URL, []*http.Cookie{{Name: "session", Value: "ok"}})
			if !tt.wantTransient {
				if err != nil {
					t.Fatalf("expected response at size limit to validate: %v", err)
				}
				return
			}
			validationErr, ok := asValidationError(err)
			if !ok {
				t.Fatalf("expected oversized response validation error, got %v", err)
			}
			if !validationErr.Transient || validationErr.ConfirmedInvalid {
				t.Fatalf("expected oversized response to fail transiently, got %+v", validationErr)
			}
			if !strings.Contains(validationErr.Error(), "exceeds") {
				t.Fatalf("expected oversized response error, got %v", validationErr)
			}
		})
	}
}

func TestValidateFFStoredCookiesTreatsBodyReadErrorAsTransient(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("friends.php"))
	}))
	defer server.Close()

	err := validateFFStoredCookies(context.Background(), server.URL, []*http.Cookie{{Name: "session", Value: "ok"}})
	validationErr, ok := asValidationError(err)
	if !ok {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !validationErr.Transient || validationErr.ConfirmedInvalid {
		t.Fatalf("expected transient read failure, got %+v", validationErr)
	}
}

func TestValidateFFStoredCookiesRejectsSuccessStatusWithoutUploadMarker(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>temporarily unavailable</html>"))
	}))
	defer server.Close()

	err := validateFFStoredCookies(context.Background(), server.URL, []*http.Cookie{{Name: "session", Value: "ok"}})
	validationErr, ok := asValidationError(err)
	if !ok {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !validationErr.ConfirmedInvalid || validationErr.Transient {
		t.Fatalf("expected missing FF upload marker to invalidate session, got %+v", validationErr)
	}
}

func TestValidateFLStoredCookiesRejectsSuccessStatusWithoutLoggedInMarker(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>maintenance page</html>"))
	}))
	defer server.Close()

	err := validateFLStoredCookies(context.Background(), server.URL, []*http.Cookie{{Name: "session", Value: "ok"}})
	validationErr, ok := asValidationError(err)
	if !ok {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !validationErr.ConfirmedInvalid || validationErr.Transient {
		t.Fatalf("expected missing FL logged-in marker to invalidate session, got %+v", validationErr)
	}
}

func TestValidateHDBStoredCookiesRejectsSuccessStatusWithoutUploadMarker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "HDB", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != hdbUploadPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>upload help</html>"))
	}))
	defer server.Close()

	err := resolveHDBStoredSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Passkey:  "passkey",
	}, dbPath, api.TrackerAuthLoginRequest{})
	validationErr, ok := asValidationError(err)
	if !ok {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !validationErr.ConfirmedInvalid || validationErr.Transient {
		t.Fatalf("expected missing HDB upload marker to invalidate session, got %+v", validationErr)
	}
}

func TestValidateHDBStoredCookiesAcceptsConcreteUploadMarker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "HDB", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != hdbUploadPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<form action="/upload/upload" method="post"><input name="file"><select name="category"></select></form>`))
	}))
	defer server.Close()

	err := resolveHDBStoredSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Passkey:  "passkey",
	}, dbPath, api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("expected HDB upload marker to validate session: %v", err)
	}
}
