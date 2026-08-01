// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
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

func TestRequestAntiCsrfTokenRejectsBlankMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		marker  string
		want    string
		wantErr bool
	}{
		{
			name:   "valid",
			marker: "token",
			want:   "token",
		},
		{
			name:    "empty",
			marker:  "",
			wantErr: true,
		},
		{
			name:    "whitespace",
			marker:  " \t ",
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`<div data-AntiCsrfToken="` + test.marker + `"></div>`))
			}))
			t.Cleanup(server.Close)

			got, err := requestAntiCsrfToken(context.Background(), server.Client(), server.URL)
			if test.wantErr {
				if err == nil {
					t.Fatalf("requestAntiCsrfToken = %q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("requestAntiCsrfToken = (%q, %v), want %q", got, err, test.want)
			}
		})
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

	err := resolveSessionForTrackerAuthAt(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, baseURL)
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
		switch {
		case r.URL.Path == ptpUploadPath:
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="verified-token"></div>`))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "new",
				Path:  "/",
			})
			_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, _, err := loginAndFetchAntiCsrfToken(context.Background(), config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, filepath.Join(t.TempDir(), "upbrr.db"), server.URL, api.NopLogger{}, api.TrackerAuthLoginRequest{})
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
			http.Redirect(w, r, "/login.php", http.StatusFound)
			return
		}
		if r.URL.Path == "/login.php" {
			_, _ = w.Write([]byte(`<form><input name="username"><input name="password"></form>`))
			return
		}
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:  "session",
			Value: "new",
			Path:  "/",
		})
		_, _ = w.Write([]byte(`{"Result":"Ok"}`))
	}))
	t.Cleanup(server.Close)

	_, _, err := resolveSession(ctx, config.TrackerConfig{
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
	var uploadPageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			uploadPageRequests.Add(1)
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="verified-token"></div>`))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "new",
				Path:  "/",
			})
			_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"login-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, token, err := resolveSession(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{})
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if token != "verified-token" {
		t.Fatalf("unexpected token %q", token)
	}
	if uploadPageRequests.Load() != 1 {
		t.Fatalf("verified upload page requests = %d, want 1", uploadPageRequests.Load())
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
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="verified-token"></div>`))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, _, err := loginAndFetchAntiCsrfToken(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{}, api.TrackerAuthLoginRequest{})
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

func TestLoginAndFetchAntiCsrfTokenDoesNotPersistUnverifiedSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="  "></div>`))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "unverified",
				Path:  "/",
			})
			_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"login-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, _, err := loginAndFetchAntiCsrfToken(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{}, api.TrackerAuthLoginRequest{})
	if err == nil || !strings.Contains(err.Error(), "verify login session") {
		t.Fatalf("expected session verification error, got %v", err)
	}
	if _, err := loadCookies(ctx, dbPath); !errors.Is(err, cookiepkg.ErrTrackerCookiesNotFound) {
		t.Fatalf("unverified login cookies were persisted: %v", err)
	}
}

func TestResolveSessionForTrackerAuthLoginUsesManual2FACode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	var gotCode string
	loginRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ptpUploadPath {
			_, _ = w.Write([]byte(`<div data-AntiCsrfToken="verified-token"></div>`))
			return
		}
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
			return
		}
		loginRequests++
		if loginRequests == 1 {
			_, _ = w.Write([]byte(`{"Result":"TfaRequired"}`))
			return
		}
		gotCode = r.FormValue("TfaCode")
		http.SetCookie(w, &http.Cookie{
			Name:  "session",
			Value: "new",
			Path:  "/",
		})
		_, _ = w.Write([]byte(`{"Result":"Ok","AntiCsrfToken":"token"}`))
	}))
	t.Cleanup(server.Close)

	err := resolveSessionForTrackerAuthLoginAt(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "654321"}, server.URL)
	if err != nil {
		t.Fatalf("ResolveSessionForTrackerAuthLogin: %v", err)
	}
	if gotCode != "654321" {
		t.Fatalf("expected manual 2FA code, got %q", gotCode)
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "new" {
		t.Fatalf("expected saved 2FA login cookies, got %#v", values)
	}
}

func TestResolveSessionReturnsMalformedLegacyCookieErrorWithoutLogin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	var jsonPath string
	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, "PTP", ".txt", ".json") {
		if filepath.Ext(candidate) == ".json" {
			jsonPath = candidate
			break
		}
	}
	if jsonPath == "" {
		t.Fatal("expected PTP JSON cookie path")
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("create cookie directory: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":`), 0o600); err != nil {
		t.Fatalf("write malformed cookies: %v", err)
	}

	var loginRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login" {
			loginRequests.Add(1)
		}
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, _, err := resolveSession(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{})
	if err == nil || !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("expected malformed cookie error, got %v", err)
	}
	if loginRequests.Load() != 0 {
		t.Fatalf("malformed cookies triggered %d login request(s)", loginRequests.Load())
	}
}

func TestResolveSessionReturnsEncryptedCookieErrorWithoutLogin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "encrypted"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE tracker_cookies SET encrypted_value = 'not-base64' WHERE tracker_id = 'PTP'`); err != nil {
		_ = db.Close()
		t.Fatalf("corrupt encrypted cookie: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite db: %v", err)
	}

	var loginRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login" {
			loginRequests.Add(1)
		}
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, _, err = resolveSession(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL, api.NopLogger{})
	if err == nil || !strings.Contains(err.Error(), "load tracker PTP from db") {
		t.Fatalf("expected encrypted cookie error, got %v", err)
	}
	if loginRequests.Load() != 0 {
		t.Fatalf("corrupt encrypted cookies triggered %d login request(s)", loginRequests.Load())
	}
}

func TestResolveSessionForTrackerAuthLoginMarksSubmitted2FARejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	loginRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			http.Redirect(w, r, "/login.php", http.StatusFound)
			return
		case r.URL.Path == "/login.php":
			_, _ = w.Write([]byte(`<form><input name="username"><input name="password"></form>`))
			return
		case r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login":
			http.NotFound(w, r)
			return
		}
		loginRequests++
		if loginRequests == 1 {
			_, _ = w.Write([]byte(`{"Result":"TfaRequired"}`))
			return
		}
		_, _ = w.Write([]byte(`{"Result":"Invalid"}`))
	}))
	t.Cleanup(server.Close)

	err := resolveSessionForTrackerAuthLoginAt(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "000000"}, server.URL)
	if !errors.Is(err, ErrSubmitted2FARejected) {
		t.Fatalf("expected submitted 2FA rejection marker, got %v", err)
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("submitted 2FA rejection must preserve stored cookies, got %#v", values)
	}
}

func TestResolveSessionForTrackerAuthLoginPreCodeFailureIsNotSubmitted2FARejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			http.Redirect(w, r, "/login.php", http.StatusFound)
			return
		case r.URL.Path == "/login.php":
			_, _ = w.Write([]byte(`<form><input name="username"><input name="password"></form>`))
			return
		case r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login":
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Result":"Invalid"}`))
	}))
	t.Cleanup(server.Close)

	err := resolveSessionForTrackerAuthLoginAt(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, api.TrackerAuthLoginRequest{Code: "000000"}, server.URL)
	if err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("expected pre-code login failed error, got %v", err)
	}
	if errors.Is(err, ErrSubmitted2FARejected) {
		t.Fatalf("pre-code login failure must not carry submitted 2FA marker: %v", err)
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("pre-code login failure must preserve stored cookies, got %#v", values)
	}
}

func TestResolveSessionForTrackerAuthLoginMissing2FACodePreservesCookies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			http.Redirect(w, r, "/login.php", http.StatusFound)
		case r.URL.Path == "/login.php":
			_, _ = w.Write([]byte(`<form><input name="username"><input name="password"></form>`))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			http.SetCookie(w, &http.Cookie{
				Name:  "session",
				Value: "new",
				Path:  "/",
			})
			_, _ = w.Write([]byte(`{"Result":"TfaRequired"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := resolveSessionForTrackerAuthLoginAt(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, api.TrackerAuthLoginRequest{}, server.URL)
	if err == nil || !strings.Contains(err.Error(), "2FA required") {
		t.Fatalf("expected missing 2FA error, got %v", err)
	}
	values, err := loadCookies(ctx, dbPath)
	if err != nil {
		t.Fatalf("loadCookies: %v", err)
	}
	if values["session"] != "existing" {
		t.Fatalf("missing 2FA code must preserve stored cookies, got %#v", values)
	}
}

func TestResolveSessionForTrackerAuthDoesNotLoginDuringTrackerOutage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newPTPAuthDB(t)
	if err := cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", map[string]string{"session": "existing"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	var loginRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == ptpUploadPath:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("<html>maintenance</html>"))
		case r.URL.Path == "/ajax.php" && r.URL.RawQuery == "action=login":
			loginRequests.Add(1)
			http.Error(w, "unexpected login", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := resolveSessionForTrackerAuthAt(ctx, config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, dbPath, server.URL)
	if err == nil || !strings.Contains(err.Error(), "status=503 response_kind=html") {
		t.Fatalf("expected safe tracker-unavailable error, got %v", err)
	}
	if loginRequests.Load() != 0 {
		t.Fatalf("tracker outage triggered %d credential login request(s)", loginRequests.Load())
	}
	values, loadErr := loadCookies(ctx, dbPath)
	if loadErr != nil {
		t.Fatalf("loadCookies: %v", loadErr)
	}
	if values["session"] != "existing" {
		t.Fatalf("tracker outage must preserve stored cookies, got %#v", values)
	}
}

func TestLoginAndFetchAntiCsrfTokenClassifiesHTMLResponseWithoutDecodeNoise(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ajax.php" || r.URL.RawQuery != "action=login" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>temporary outage</html>"))
	}))
	t.Cleanup(server.Close)

	_, _, err := resolveSession(context.Background(), config.TrackerConfig{
		Username:    "user",
		Password:    "pass",
		AnnounceURL: "https://please.passthepopcorn.me/passkey/announce",
	}, newPTPAuthDB(t), server.URL, api.NopLogger{})
	if err == nil || !strings.Contains(err.Error(), "status=200 response_kind=html") {
		t.Fatalf("expected safe HTML-response classification, got %v", err)
	}
	if strings.Contains(err.Error(), "invalid character") || strings.Contains(err.Error(), "temporary outage") {
		t.Fatalf("HTML-response error exposed parser or remote-body detail: %v", err)
	}
}

func newPTPAuthDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	authfixture.Write(t, dbPath)
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
