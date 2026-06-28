// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveGroupDescriptionSkipsTVDBForMovie(t *testing.T) {
	got := resolveGroupDescription(api.PreparedMetadata{
		MediaInfoCategory: "TV",
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE", TVDBID: 456},
	})
	if strings.Contains(got, "thetvdb.com") {
		t.Fatalf("did not expect tvdb link for movie description, got %q", got)
	}
}

func TestResolveGroupDescriptionIncludesTVDBForTV(t *testing.T) {
	got := resolveGroupDescription(api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV", TVDBID: 456},
	})
	if !strings.Contains(got, "thetvdb.com/?id=456") {
		t.Fatalf("expected tvdb link for TV description, got %q", got)
	}
}

func TestExtractMTVAuthKeyMatchesUploadAssistantShape(t *testing.T) {
	t.Parallel()

	auth := "abcdefghijklmnopqrstuvwxyzABCDEF"
	tests := map[string]string{
		"plain":       "authkey=" + auth,
		"query tail":  "https://example.test/upload?authkey=" + auth + "&x=1",
		"last marker": "authkey=oldoldoldoldoldoldoldoldoldoldoldold authkey=" + auth + `">`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := extractMTVAuthKey(body); got != auth {
				t.Fatalf("expected %q, got %q", auth, got)
			}
		})
	}
}

func TestResolveSessionForTrackerAuthPreservesCookiesOnTransientAuthFetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	if err := saveMTVCookies(ctx, dbPath, map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("saveMTVCookies: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()

	err := ResolveSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:      baseURL,
		Username: "user",
		Password: "pass",
	}, dbPath)
	if err == nil {
		t.Fatal("expected transient auth fetch error")
	}
	got, loadErr := loadMTVCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadMTVCookies after transient error: %v", loadErr)
	}
	if got["session"] != "abc" {
		t.Fatalf("expected transient failure to preserve cookies, got %#v", got)
	}
}

func TestResolveSessionForTrackerAuthReportsPostLoginCookiePersistenceFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
		case "/index.php":
			_, _ = w.Write([]byte(`authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuth(context.Background(), config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, filepath.Join(t.TempDir(), "upbrr.db"))
	if !errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
		t.Fatalf("expected auth helper unavailable error, got %v", err)
	}
	if !strings.Contains(err.Error(), "persist cookies after successful login") {
		t.Fatalf("expected distinct persistence failure, got %v", err)
	}
}

func TestResolveSessionForTrackerAuthSavesCookiesWhenLoginResponseContainsAuthKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	indexRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh", Path: "/"})
			_, _ = w.Write([]byte(`authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
		case "/index.php":
			indexRequests++
			_, _ = w.Write([]byte(`<html>logged out</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath)
	if err != nil {
		t.Fatalf("ResolveSessionForTrackerAuth: %v", err)
	}
	if indexRequests != 0 {
		t.Fatalf("expected login response authkey to avoid index lookup, got %d", indexRequests)
	}
	got, err := loadMTVCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadMTVCookies: %v", err)
	}
	if got["session"] != "fresh" {
		t.Fatalf("expected saved login cookies, got %#v", got)
	}
}

func TestLoginAndResolveAuthKeyReturnsLoginPageReadError(t *testing.T) {
	t.Parallel()

	handlerErrs := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodGet:
			if err := writeMalformedChunkedResponse(w); err != nil {
				recordHandlerError(handlerErrs, "write malformed chunked response: %v", err)
			}
		default:
			recordHandlerError(handlerErrs, "unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, _, _, _, err := loginAndResolveAuthKey(context.Background(), config.TrackerConfig{
		Username: "user",
		Password: "pass",
	}, server.URL, api.TrackerAuthLoginRequest{})
	assertNoHandlerError(t, handlerErrs)
	if err == nil {
		t.Fatal("expected login page read error")
	}
	if !strings.Contains(err.Error(), "read login page response") {
		t.Fatalf("expected login page read-error context, got %v", err)
	}
}

func TestLoginAndResolveAuthKeyReturnsLoginPostReadError(t *testing.T) {
	t.Parallel()

	handlerErrs := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
		case r.URL.Path == "/login" && r.Method == http.MethodPost:
			if err := writeMalformedChunkedResponse(w); err != nil {
				recordHandlerError(handlerErrs, "write malformed chunked response: %v", err)
			}
		default:
			recordHandlerError(handlerErrs, "unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, _, _, _, err := loginAndResolveAuthKey(context.Background(), config.TrackerConfig{
		Username: "user",
		Password: "pass",
	}, server.URL, api.TrackerAuthLoginRequest{})
	assertNoHandlerError(t, handlerErrs)
	if err == nil {
		t.Fatal("expected login response read error")
	}
	if !strings.Contains(err.Error(), "read login response") {
		t.Fatalf("expected login response read-error context, got %v", err)
	}
}

func TestResolveSessionForTrackerAuthPostsLoginToRedirectedHost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	var canonicalURL string
	postedCanonicalLogin := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodGet && strings.HasPrefix(r.Host, "localhost:"):
			http.Redirect(w, r, canonicalURL+"/login", http.StatusFound)
		case r.URL.Path == "/login" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
		case r.URL.Path == "/login" && r.Method == http.MethodPost && strings.HasPrefix(r.Host, "localhost:"):
			t.Fatalf("login POST used original host")
		case r.URL.Path == "/login" && r.Method == http.MethodPost:
			postedCanonicalLogin = true
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh", Path: "/"})
			_, _ = w.Write([]byte(`authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	canonicalURL = server.URL
	sourceURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	err := ResolveSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:      sourceURL,
		Username: "user",
		Password: "pass",
	}, dbPath)
	if err != nil {
		t.Fatalf("ResolveSessionForTrackerAuth: %v", err)
	}
	if !postedCanonicalLogin {
		t.Fatal("expected login POST on redirected host")
	}
	got, err := loadMTVCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadMTVCookies: %v", err)
	}
	if got["session"] != "fresh" {
		t.Fatalf("expected saved redirected-login cookies, got %#v", got)
	}
}

func TestUploadPostsToRedirectedLoginHost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	torrentPath := filepath.Join(t.TempDir(), "release.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	var canonicalURL string
	postedCanonicalUpload := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodGet && strings.HasPrefix(r.Host, "localhost:"):
			http.Redirect(w, r, canonicalURL+"/login", http.StatusFound)
		case r.URL.Path == "/login" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
		case r.URL.Path == "/login" && r.Method == http.MethodPost:
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh", Path: "/"})
			_, _ = w.Write([]byte(`authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
		case r.URL.Path == mtvUploadPath && r.Method == http.MethodPost && strings.HasPrefix(r.Host, "localhost:"):
			t.Fatalf("upload POST used original host")
		case r.URL.Path == mtvUploadPath && r.Method == http.MethodPost:
			postedCanonicalUpload = true
			if _, err := r.Cookie("session"); err != nil {
				t.Fatalf("expected session cookie on canonical upload: %v", err)
			}
			http.Redirect(w, r, canonicalURL+"/torrents.php?id=1", http.StatusFound)
		case r.URL.Path == "/torrents.php":
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	canonicalURL = server.URL
	sourceURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	summary, err := upload(ctx, trackers.UploadRequest{
		Tracker: "MTV",
		Meta: api.PreparedMetadata{
			TorrentPath: torrentPath,
			ReleaseName: "Release",
		},
		TrackerConfig: config.TrackerConfig{
			URL:      sourceURL,
			Username: "user",
			Password: "pass",
		},
		AppConfig: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		Assets:    &trackers.DescriptionAssets{Final: true, Description: "desc"},
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if summary.Uploaded != 1 {
		t.Fatalf("expected upload success, got %#v", summary)
	}
	if !postedCanonicalUpload {
		t.Fatal("expected upload POST on redirected host")
	}
}

func TestResolveSessionForTrackerAuthRejectsEmptyLoginCookiesWithoutReplacingStoredCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	if err := saveMTVCookies(ctx, dbPath, map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("saveMTVCookies: %v", err)
	}
	indexRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			_, _ = w.Write([]byte(`<html>ok</html>`))
		case "/index.php":
			indexRequests++
			if indexRequests == 1 {
				_, _ = w.Write([]byte(`<html>logged out</html>`))
				return
			}
			_, _ = w.Write([]byte(`authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuth(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath)
	if err == nil || !strings.Contains(err.Error(), "no usable cookies") {
		t.Fatalf("expected empty login cookie error, got %v", err)
	}
	got, loadErr := loadMTVCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadMTVCookies after empty login cookies: %v", loadErr)
	}
	if got["session"] != "existing" {
		t.Fatalf("expected empty login cookies to preserve stored cookies, got %#v", got)
	}
}

func TestResolveSessionForTrackerAuthLoginUsesManual2FACode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	var gotCode string
	indexRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			indexRequests++
			_, _ = w.Write([]byte(`<html>logged out</html>`))
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.Redirect(w, r, "/twofactor/login", http.StatusFound)
		case "/twofactor/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="ponmlkjihgfedcba">`))
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			gotCode = r.FormValue("code")
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte(`authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "654321"})
	if err != nil {
		t.Fatalf("ResolveSessionForTrackerAuthLogin: %v", err)
	}
	if gotCode != "654321" {
		t.Fatalf("expected manual 2FA code, got %q", gotCode)
	}
	if indexRequests != 0 {
		t.Fatalf("expected 2FA response authkey to avoid index lookup, got %d", indexRequests)
	}
	got, err := loadMTVCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadMTVCookies: %v", err)
	}
	if got["session"] != "new" {
		t.Fatalf("expected saved 2FA login cookies, got %#v", got)
	}
}

func TestResolveSessionForTrackerAuthLoginMarksSubmitted2FAAuthKeyMiss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	if err := saveMTVCookies(ctx, dbPath, map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("saveMTVCookies: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			_, _ = w.Write([]byte(`<html>logged out</html>`))
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.Redirect(w, r, "/twofactor/login", http.StatusFound)
		case "/twofactor/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="ponmlkjihgfedcba">`))
				return
			}
			_, _ = w.Write([]byte(`<html>invalid code</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "000000"})
	if !errors.Is(err, ErrSubmitted2FARejected) {
		t.Fatalf("expected submitted 2FA rejection marker, got %v", err)
	}
	if !strings.Contains(err.Error(), "final_path=/twofactor/login status=200") || !strings.Contains(err.Error(), "path=/index.php status=200") {
		t.Fatalf("expected safe path/status diagnostics, got %v", err)
	}
	got, loadErr := loadMTVCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadMTVCookies: %v", loadErr)
	}
	if got["session"] != "existing" {
		t.Fatalf("submitted 2FA rejection must preserve stored cookies, got %#v", got)
	}
}

func TestResolveSessionForTrackerAuthLoginReturns2FAReadError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	if err := saveMTVCookies(ctx, dbPath, map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("saveMTVCookies: %v", err)
	}
	handlerErrs := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			_, _ = w.Write([]byte(`<html>logged out</html>`))
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.Redirect(w, r, "/twofactor/login", http.StatusFound)
		case "/twofactor/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="ponmlkjihgfedcba">`))
				return
			}
			if err := writeMalformedChunkedResponse(w); err != nil {
				recordHandlerError(handlerErrs, "write malformed chunked response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "123456"})
	assertNoHandlerError(t, handlerErrs)
	if err == nil {
		t.Fatal("expected 2FA response read error")
	}
	if errors.Is(err, ErrSubmitted2FARejected) {
		t.Fatalf("read error must not be classified as submitted 2FA rejection: %v", err)
	}
	if !strings.Contains(err.Error(), "read 2FA login response") {
		t.Fatalf("expected read-error context, got %v", err)
	}
	got, loadErr := loadMTVCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadMTVCookies: %v", loadErr)
	}
	if got["session"] != "existing" {
		t.Fatalf("2FA read error must preserve stored cookies, got %#v", got)
	}
}

func TestResolveSessionForTrackerAuthLoginReportsSafeAuthKeyDiagnostics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "new", Path: "/"})
			_, _ = w.Write([]byte(`<html>login accepted but no upload token secret-response-body</html>`))
		case "/index.php":
			_, _ = w.Write([]byte(`<html>logged in but no upload token other-secret-body</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	if !errors.Is(err, errMTVAuthKeyNotFound) {
		t.Fatalf("expected auth key miss, got %v", err)
	}
	if !strings.Contains(err.Error(), "final_path=/login status=200") || !strings.Contains(err.Error(), "path=/index.php status=200") {
		t.Fatalf("expected path/status diagnostics, got %v", err)
	}
	for _, secret := range []string{"secret-response-body", "other-secret-body"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("diagnostic leaked response body %q: %v", secret, err)
		}
	}
}

func TestResolveSessionForTrackerAuthLoginMissing2FACodePreservesCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newMTVAuthDB(t)
	if err := saveMTVCookies(ctx, dbPath, map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("saveMTVCookies: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			_, _ = w.Write([]byte(`<html>logged out</html>`))
		case "/login":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">`))
				return
			}
			http.Redirect(w, r, "/twofactor/login", http.StatusFound)
		case "/twofactor/login":
			_, _ = w.Write([]byte(`<input name="token" value="ponmlkjihgfedcba">`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := ResolveSessionForTrackerAuthLogin(ctx, config.TrackerConfig{
		URL:      server.URL,
		Username: "user",
		Password: "pass",
	}, dbPath, api.TrackerAuthLoginRequest{})
	if err == nil || !strings.Contains(err.Error(), "2FA required") {
		t.Fatalf("expected missing 2FA error, got %v", err)
	}
	got, loadErr := loadMTVCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadMTVCookies: %v", loadErr)
	}
	if got["session"] != "existing" {
		t.Fatalf("missing 2FA code must preserve stored cookies, got %#v", got)
	}
}

func newMTVAuthDB(t *testing.T) string {
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

// recordHandlerError preserves handler failures for assertion from the test
// goroutine, where testing.T failures are safe to report.
func recordHandlerError(errs chan<- string, format string, args ...any) {
	select {
	case errs <- fmt.Sprintf(format, args...):
	default:
	}
}

// assertNoHandlerError fails the test if the server handler recorded a setup or
// routing error while servicing the request under test.
func assertNoHandlerError(t *testing.T, errs <-chan string) {
	t.Helper()

	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

// writeMalformedChunkedResponse sends an intentionally truncated chunked body so
// callers exercise response-body read errors after headers are received.
func writeMalformedChunkedResponse(w http.ResponseWriter) error {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("response writer does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return fmt.Errorf("hijack response: %w", err)
	}
	if _, err := rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nTransfer-Encoding: chunked\r\n\r\n20\r\npartial"); err != nil {
		_ = conn.Close()
		return fmt.Errorf("write response: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("flush response: %w", err)
	}
	_ = conn.Close()
	return nil
}
