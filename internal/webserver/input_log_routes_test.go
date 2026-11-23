// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
)

func TestInputLogBrowserDownloadNeedsSessionAndKeepsAttemptScope(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "upbrr.db"))
	logger, err := logging.New(config.LoggingConfig{Level: "info"}, server.backend.repo.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	server.backend.logger = logger
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "Example.mkv")
	first, _, finish := logger.BeginInputLog(ctx, server.backend.repo, source, "open input")
	logging.FromContext(first, logger).Infof("old attempt")
	finish("", "failed")
	current, id, complete := logger.BeginInputLog(ctx, server.backend.repo, source, "open input")
	logging.FromContext(current, logger).Infof("current attempt")
	complete("", "completed")

	active, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.registerInputLogRoutes(mux)
	path := "/api/app/InputLog/" + id + "?download=1"
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	unauth := httptest.NewRecorder()
	mux.ServeHTTP(unauth, request)
	if unauth.Code != http.StatusUnauthorized || strings.Contains(unauth.Body.String(), "current attempt") {
		t.Fatalf("unauthenticated result: %d %s", unauth.Code, unauth.Body.String())
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
	auth := httptest.NewRecorder()
	mux.ServeHTTP(auth, request)
	text := auth.Body.String()
	if auth.Code != http.StatusOK || !strings.HasPrefix(auth.Header().Get("Content-Disposition"), "attachment;") ||
		!strings.Contains(text, "current attempt") || strings.Contains(text, "old attempt") ||
		!strings.Contains(text, "Coverage: memory") {
		t.Fatalf("download: %d %v %s", auth.Code, auth.Header(), text)
	}
}
