// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
)

func TestRouteImportConfigAcceptsEscapedEnvelopeAtRawCap(t *testing.T) {
	t.Parallel()

	repo, dbPath := openBrowseTestRepo(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	server := testServerWithBackend(t, repo, config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})
	content := "#" + strings.Repeat("<", configImportMaxBytes-1)
	body, err := json.Marshal(map[string]string{
		"FileName":    "config.yaml",
		"FileContent": content,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if len(body) <= configImportMaxBytes+1024*1024 {
		t.Fatalf("test envelope did not exceed prior cap: got %d", len(body))
	}

	mux := http.NewServeMux()
	server.registerAppRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, configImportRouteRequest(body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected escaped envelope import to succeed, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestRouteImportConfigRejectsEnvelopeOverRouteCap(t *testing.T) {
	t.Parallel()

	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)
	body := append(bytes.Repeat([]byte(" "), configImportRequestEnvelopeMaxBytes+1), 'x')
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, configImportRouteRequest(body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected route cap rejection, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "request body too large") {
		t.Fatalf("expected body-too-large error, got %s", recorder.Body.String())
	}
}

func configImportRouteRequest(body []byte) *http.Request {
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/app/ImportConfig",
		bytes.NewReader(body),
	)
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = "127.0.0.1:5050"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("X-Csrf-Token", "test-csrf")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	return req
}
