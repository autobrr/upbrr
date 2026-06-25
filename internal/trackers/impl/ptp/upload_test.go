// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLoadCookiesSuccessReturnsNilError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}

	got, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if got["session"] != "abc" {
		t.Fatalf("unexpected cookies: %#v", got)
	}
}

func TestResolveSessionForTrackerAuthPreservesCookiesOnTransientTokenFetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()

	err := ResolveSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:         baseURL,
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath)
	if err == nil {
		t.Fatal("expected transient token fetch error")
	}
	got, loadErr := loadCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadCookies after transient error: %v", loadErr)
	}
	if got["session"] != "abc" {
		t.Fatalf("expected transient failure to preserve cookies, got %#v", got)
	}
}

func TestLoginAndFetchAntiCsrfTokenReturnsPersistenceError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
		_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"token"}`))
	}))
	t.Cleanup(server.Close)

	_, _, err := resolveSession(context.Background(), config.TrackerConfig{
		URL:         server.URL,
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, filepath.Join(t.TempDir(), "upbrr.db"), server.URL, api.NopLogger{})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if !strings.Contains(err.Error(), "persist login cookies") {
		t.Fatalf("expected persistence error, got %v", err)
	}
}

func TestLoginAndFetchAntiCsrfTokenDoesNotOverwriteCookiesWhenTokenMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ptpUploadPath {
			_, _ = w.Write([]byte("<html>logged out</html>"))
			return
		}
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
		_, _ = w.Write([]byte(`{"Result":"Ok"}`))
	}))
	t.Cleanup(server.Close)

	_, _, err := resolveSession(ctx, config.TrackerConfig{
		URL:         server.URL,
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("missing token must not overwrite stored cookies, got %#v", values)
	}
}

func TestLoginAndFetchAntiCsrfTokenPersistsCookiesAfterTokenGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
		_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"token"}`))
	}))
	t.Cleanup(server.Close)

	_, token, err := resolveSession(ctx, config.TrackerConfig{
		URL:         server.URL,
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{})
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if token != "token" {
		t.Fatalf("unexpected token %q", token)
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "new" {
		t.Fatalf("expected saved login cookies, got %#v", values)
	}
}

func TestLoginAndFetchAntiCsrfTokenRejectsEmptyJarWithoutReplacingCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			_, _ = w.Write([]byte("<html>logged out</html>"))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, _, err := resolveSession(ctx, config.TrackerConfig{
		URL:         server.URL,
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{})
	if err == nil || !strings.Contains(err.Error(), "no usable cookies") {
		t.Fatalf("expected empty cookie jar error, got %v", err)
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("empty login jar must preserve stored cookies, got %#v", values)
	}
}

func newPTPAuthDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		t.Fatalf("MigrateContext: %v", err)
	}
	_ = repo.Close()
	return dbPath
}
