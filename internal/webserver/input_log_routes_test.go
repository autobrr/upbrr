// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
)

func TestInputLogHistoryAndDownloadCombineSameInput(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "upbrr.db"))
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = server.backend.repo.DBPath()
	cfg.TorrentClients = nil
	cfg.Logging.Level = "warn"
	generation, err := buildRuntimeGeneration(t.Context(), *cfg, server.backend.repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(generation.Bundle.retire)
	server.backend.capabilities = generation.Capabilities
	logger := generation.Logger
	logger.SetConsoleOutput(io.Discard, io.Discard)
	server.backend.logger = logger
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "Example.mkv")
	if err := server.backend.repo.Save(ctx, db.FileMetadata{Path: source, Title: "Example"}); err != nil {
		t.Fatal(err)
	}
	first, _, finish := logger.BeginInputLog(ctx, server.backend.repo, source, "open input")
	logging.FromContext(first, logger).Tracef("old attempt")
	finish("", "failed")
	operationPath := source
	if runtime.GOOS == "windows" {
		operationPath = strings.ToLower(filepath.ToSlash(source))
	}
	current, id, complete := logger.BeginInputLog(ctx, server.backend.repo, operationPath, "preparation")
	logging.FromContext(current, logger).Infof("current attempt")
	complete("", "completed")
	otherPath := filepath.Join(filepath.Dir(source), "example.mkv")
	if runtime.GOOS == "windows" {
		otherPath = filepath.Join(filepath.Dir(source), "Other.mkv")
	}
	other, _, finishOther := logger.BeginInputLog(ctx, server.backend.repo, otherPath, "open input")
	logging.FromContext(other, logger).Infof("unrelated input")
	finishOther("", "failed")

	active, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)
	path := "/api/app/InputLog/" + id
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
		!strings.Contains(text, "current attempt") || !strings.Contains(text, "old attempt") || strings.Contains(text, "unrelated input") ||
		!strings.Contains(text, "Coverage: memory") {
		t.Fatalf("download: %d %v %s", auth.Code, auth.Header(), text)
	}
	previewBody, err := json.Marshal(struct{ SourcePath string }{source})
	if err != nil {
		t.Fatal(err)
	}
	previewRequest := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/app/GetInputLog", strings.NewReader(string(previewBody)))
	previewRequest.Host = "127.0.0.1:7480"
	previewRequest.Header.Set("Origin", "http://127.0.0.1:7480")
	previewRequest.Header.Set("X-Csrf-Token", active.CSRFToken)
	previewRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
	preview := httptest.NewRecorder()
	mux.ServeHTTP(preview, previewRequest)
	var previewData struct {
		logging.InputLogExport
		DownloadID string
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewData); err != nil || preview.Code != http.StatusOK ||
		previewData.DownloadID != id || previewData.Text != text {
		t.Fatalf("preview differs from download: %d %s (%v)", preview.Code, preview.Body.String(), err)
	}
	listRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/app/ListInputHistory", nil)
	listRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
	list := httptest.NewRecorder()
	mux.ServeHTTP(list, listRequest)
	var history inputHistoryList
	if err := json.Unmarshal(list.Body.Bytes(), &history); err != nil || list.Code != http.StatusOK || history.LogsUnavailable ||
		len(history.Entries) != 2 || history.Entries[0].LogsOnly || history.Entries[0].SourcePath != source ||
		!history.Entries[1].LogsOnly || history.Entries[1].SourcePath != otherPath {
		t.Fatalf("history split or lost input: %d %s (%v)", list.Code, list.Body.String(), err)
	}
	if err := server.backend.repo.PurgeContentData(ctx, source); err != nil {
		t.Fatal(err)
	}
	if paths, err := server.backend.repo.InputLogSources(ctx, nil); err != nil || len(paths) != 1 || paths[0] != otherPath {
		t.Fatalf("deleting history left a log-only duplicate or removed another input: %v, %v", paths, err)
	}
}
