// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackerauth"
)

func TestBackendImportTrackerAuthCookieContentRejectsOverCap(t *testing.T) {
	t.Parallel()

	_, err := (&Backend{}).ImportTrackerAuthCookieContent(
		context.Background(),
		"AR",
		"cookies.txt",
		strings.Repeat("x", trackerauth.MaxCookieImportContentBytes+1),
	)
	if err == nil {
		t.Fatal("expected over-cap import error")
	}
	if !strings.Contains(err.Error(), "cookie file content exceeds") {
		t.Fatalf("expected shared content cap error, got %v", err)
	}
}

func TestRouteImportTrackerAuthCookieContentAcceptsEscapedEnvelopeAtRawCap(t *testing.T) {
	t.Parallel()

	repo, dbPath := openBrowseTestRepo(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	server := testServerWithBackend(t, repo, config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})
	prefix := ".example.test\tTRUE\t/\tTRUE\t0\tsession\t"
	content := prefix + strings.Repeat("<", trackerauth.MaxCookieImportContentBytes-len(prefix))
	body, err := json.Marshal(map[string]string{
		"Tracker":  "AR",
		"FileName": "cookies.txt",
		"Content":  content,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if len(body) <= trackerauth.MaxCookieImportContentBytes+64*1024 {
		t.Fatalf("test envelope did not exceed prior cap: got %d", len(body))
	}

	mux := http.NewServeMux()
	server.registerAppRoutes(mux)
	req := trackerAuthImportRouteRequest(body)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected escaped envelope import to succeed, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestRouteImportTrackerAuthCookieContentRejectsEnvelopeOverRouteCap(t *testing.T) {
	t.Parallel()

	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)
	req := trackerAuthImportRouteRequest([]byte(strings.Repeat(" ", cookieImportRequestEnvelopeMaxBytes+1) + "x"))
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected route cap rejection, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "request body too large") {
		t.Fatalf("expected body-too-large error, got %s", recorder.Body.String())
	}
}

func trackerAuthImportRouteRequest(body []byte) *http.Request {
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/app/ImportTrackerAuthCookieContent",
		strings.NewReader(string(body)),
	)
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = "127.0.0.1:5050"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("X-Csrf-Token", "test-csrf")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	return req
}
