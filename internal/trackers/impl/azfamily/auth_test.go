// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAuthSessionResolverIsValidationOnly(t *testing.T) {
	t.Parallel()

	for _, tracker := range []string{"AZ", "CZ", "PHD"} {
		definition := New(tracker)
		capability := definition.AuthCapability()
		if capability.SupportsLogin || capability.SupportsAutoLogin {
			t.Errorf("%s must not advertise credential login: %#v", tracker, capability)
		}
		if definition.AuthSessionResolver() == nil {
			t.Errorf("%s must expose imported-cookie validation", tracker)
		}
	}
}

func TestAuthSessionResolverClassifiesCookieValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		status           int
		body             string
		storeCookies     bool
		wantInvalid      bool
		wantTransient    bool
		wantAuthRequired bool
	}{
		{
			name:         "ready",
			status:       http.StatusOK,
			body:         `<meta name="_token" content="secret-token">`,
			storeCookies: true,
		},
		{
			name:         "expired",
			status:       http.StatusOK,
			body:         `<form action="/login"><input name="email"><input name="password"></form>`,
			storeCookies: true,
			wantInvalid:  true,
		},
		{
			name:          "cloudflare challenge",
			status:        http.StatusForbidden,
			body:          `<title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`,
			storeCookies:  true,
			wantTransient: true,
		},
		{
			name:             "missing cookies",
			status:           http.StatusOK,
			body:             `<meta name="_token" content="secret-token">`,
			wantAuthRequired: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedCookie atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if cookie, err := r.Cookie("session"); err == nil && cookie.Value == "cookievalue" {
					receivedCookie.Store(true)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			dbPath := filepath.Join(t.TempDir(), "upbrr.db")
			if tt.storeCookies {
				writeAZAuthCookies(t, dbPath, "CZ")
			}
			definition := &Definition{site: siteDefinition{Name: "CZ", BaseURL: server.URL}}
			err := definition.AuthSessionResolver()(t.Context(), config.TrackerConfig{}, dbPath, api.TrackerAuthLoginRequest{})
			if !tt.wantInvalid && !tt.wantTransient && !tt.wantAuthRequired {
				if err != nil {
					t.Fatalf("validate cookies: %v", err)
				}
			} else {
				var resolution *trackers.AuthResolutionError
				if !errors.As(err, &resolution) {
					t.Fatalf("expected auth resolution error, got %v", err)
				}
				if resolution.ConfirmedInvalid != tt.wantInvalid || resolution.Transient != tt.wantTransient ||
					resolution.AuthRequired != tt.wantAuthRequired {
					t.Fatalf("unexpected auth resolution: %#v", resolution)
				}
			}
			if tt.storeCookies && !receivedCookie.Load() {
				t.Fatal("stored cookie was not sent")
			}
		})
	}
}

func writeAZAuthCookies(t *testing.T, dbPath string, tracker string) {
	t.Helper()
	cookieDir := filepath.Join(filepath.Dir(dbPath), "cookies")
	if err := os.MkdirAll(cookieDir, 0o755); err != nil {
		t.Fatalf("create cookie directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cookieDir, tracker+".json"), []byte(`{"session":"cookievalue"}`), 0o600); err != nil {
		t.Fatalf("write cookies: %v", err)
	}
}
