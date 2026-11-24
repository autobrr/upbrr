// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
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
	subscriptionID, live := logger.Subscribe(10)
	t.Cleanup(func() { logger.Unsubscribe(subscriptionID) })
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
	logger.Warnf("live warning")
	if recent := logger.Recent(0); len(recent) != 1 || recent[0].Message != "live warning" {
		t.Fatalf("trace input capture leaked into recent application logs: %#v", recent)
	}
	select {
	case first := <-live:
		if first.Message != "live warning" || len(live) != 0 {
			t.Fatalf("live subscriber received input-only entries: %#v", first)
		}
	default:
		t.Fatal("live warning did not reach subscriber")
	}

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
	server.backend.capabilities.History = nil
	partial := httptest.NewRecorder()
	mux.ServeHTTP(partial, listRequest)
	var logsOnly inputHistoryList
	if err := json.Unmarshal(partial.Body.Bytes(), &logsOnly); err != nil || partial.Code != http.StatusOK ||
		!logsOnly.HistoryUnavailable || logsOnly.LogsUnavailable || len(logsOnly.Entries) != 2 ||
		!logsOnly.Entries[0].LogsOnly || !logsOnly.Entries[1].LogsOnly {
		t.Fatalf("history without Core: %d %s (%v)", partial.Code, partial.Body.String(), err)
	}
	if err := server.backend.repo.PurgeContentData(ctx, source); err != nil {
		t.Fatal(err)
	}
	if paths, err := server.backend.repo.InputLogSources(ctx, nil); err != nil || len(paths) != 1 || paths[0] != otherPath {
		t.Fatalf("deleting history left a log-only duplicate or removed another input: %v, %v", paths, err)
	}
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, request)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted input log download returned %d, want 404", missing.Code)
	}
	if err := server.backend.repo.Close(); err != nil {
		t.Fatal(err)
	}
	unavailable := httptest.NewRecorder()
	mux.ServeHTTP(unavailable, request)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable input log lookup returned %d, want 503", unavailable.Code)
	}
}

func TestDeleteInputLogsRemovesFailedParentWithoutErasingChildRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "delete-input-logs.db")
	backend, err := NewBackendWithContext(ctx, backendConfigTestConfig(dbPath), newEventHub())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	backend.currentLogger().SetConsoleOutput(io.Discard, io.Discard)

	sessions, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessions.Close)
	active, err := sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		backend:        backend,
		sessions:       sessions,
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	mediaRoot := t.TempDir()
	parentDir := filepath.Join(mediaRoot, "Parent.Collection.2026-GRP")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childFile := filepath.Join(parentDir, "Child.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(childFile, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stored child release + upload record + its own input log attempt.
	if err := backend.repo.Save(ctx, db.FileMetadata{
		Path:  childFile,
		Title: "Child Release",
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.repo.CreateUploadRecord(ctx, db.UploadRecord{
		Tracker:    "BLU",
		Status:     "uploaded",
		SourcePath: childFile,
	}); err != nil {
		t.Fatal(err)
	}
	childCtx, _, finishChild := backend.currentLogger().BeginInputLog(ctx, backend.repo, childFile, "preparation")
	logging.FromContext(childCtx, backend.currentLogger()).Infof("child prepared")
	finishChild("", "completed")

	// Failed parent directory attempt that appears as a log-only History item.
	parentCtx, _, finishParent := backend.currentLogger().BeginInputLog(ctx, backend.repo, parentDir, "open input")
	logging.FromContext(parentCtx, backend.currentLogger()).Warnf("parent open failed")
	finishParent("", "failed")

	postDelete := func(body string, withCookie, withCSRF bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/app/DeleteInputLogs", strings.NewReader(body))
		req.Host = "127.0.0.1:7480"
		req.Header.Set("Origin", "http://127.0.0.1:7480")
		if withCSRF {
			req.Header.Set("X-Csrf-Token", active.CSRFToken)
		}
		if withCookie {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	payload, err := json.Marshal(struct{ SourcePath string }{parentDir})
	if err != nil {
		t.Fatal(err)
	}
	if rec := postDelete(string(payload), false, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
	if rec := postDelete(string(payload), true, false); rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want 403", rec.Code)
	}
	getReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/app/DeleteInputLogs", nil)
	getReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", getRec.Code)
	}
	if rec := postDelete(`{"SourcePath":"   "}`, true, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank path status = %d, want 400", rec.Code)
	}

	if rec := postDelete(string(payload), true, true); rec.Code != http.StatusOK {
		t.Fatalf("DeleteInputLogs status = %d: %s", rec.Code, rec.Body.String())
	}

	// Parent log-only row is gone, while stored child release, upload record,
	// and child input logs remain intact.
	listReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/app/ListInputHistory", nil)
	listReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	var history inputHistoryList
	if err := json.Unmarshal(listRec.Body.Bytes(), &history); err != nil || listRec.Code != http.StatusOK ||
		len(history.Entries) != 1 || history.Entries[0].LogsOnly || history.Entries[0].SourcePath != childFile {
		t.Fatalf("ListInputHistory after log-only delete = %d %s (%v)", listRec.Code, listRec.Body.String(), err)
	}
	if meta, err := backend.repo.GetByPath(ctx, childFile); err != nil || meta.Title != "Child Release" {
		t.Fatalf("child release metadata lost: %#v, %v", meta, err)
	}
	if uploads, err := backend.repo.ListUploadHistoryByPath(ctx, childFile); err != nil || len(uploads) != 1 || uploads[0].Tracker != "BLU" {
		t.Fatalf("child upload history lost: %#v, %v", uploads, err)
	}
	if childLogs, err := backend.repo.InputLogs(ctx, childFile, ""); err != nil || len(childLogs) != 1 {
		t.Fatalf("child input logs lost: %#v, %v", childLogs, err)
	}
}

func TestInputLogsSurviveRuntimeActivationSettingsTransition(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "settings-transition.db")
	initialCfg := backendConfigTestConfig(dbPath)
	initialCfg.Logging.FileEnabled = false
	initialCfg.Logging.Level = "warn"

	backend, err := NewBackendWithContext(ctx, initialCfg, newEventHub())
	if err != nil {
		t.Fatal(err)
	}
	closedBackend := false
	t.Cleanup(func() {
		if !closedBackend {
			_ = backend.Close()
		}
	})
	backend.currentLogger().SetConsoleOutput(io.Discard, io.Discard)

	sessions, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessions.Close)
	active, err := sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		backend:        backend,
		sessions:       sessions,
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	source := filepath.Join(t.TempDir(), "Failed.MemoryOnly.2026-GRP.mkv")
	scoped, attemptID, finish := backend.currentLogger().BeginInputLog(ctx, backend.repo, source, "open input")
	logging.FromContext(scoped, backend.currentLogger()).Tracef("memory-only failure detail")
	finish("", "failed")

	assertHistoryPreviewAndDownload := func(label string) {
		t.Helper()
		previewBody, err := json.Marshal(struct{ SourcePath string }{source})
		if err != nil {
			t.Fatal(err)
		}
		previewReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/app/GetInputLog", strings.NewReader(string(previewBody)))
		previewReq.Host = "127.0.0.1:7480"
		previewReq.Header.Set("Origin", "http://127.0.0.1:7480")
		previewReq.Header.Set("X-Csrf-Token", active.CSRFToken)
		previewReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
		previewRec := httptest.NewRecorder()
		mux.ServeHTTP(previewRec, previewReq)
		var previewData struct {
			logging.InputLogExport
			DownloadID string
		}
		if err := json.Unmarshal(previewRec.Body.Bytes(), &previewData); err != nil || previewRec.Code != http.StatusOK ||
			previewData.Coverage != "memory" || previewData.DownloadID != attemptID ||
			!strings.Contains(previewData.Text, "memory-only failure detail") {
			t.Fatalf("%s preview = %d %s (%v)", label, previewRec.Code, previewRec.Body.String(), err)
		}

		//pathpolicy:allow HTTP route path, not a local filesystem path.
		downloadPath := "/api/app/InputLog/" + attemptID
		downloadReq := httptest.NewRequestWithContext(ctx, http.MethodGet, downloadPath, nil)
		downloadReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
		downloadRec := httptest.NewRecorder()
		mux.ServeHTTP(downloadRec, downloadReq)
		if downloadRec.Code != http.StatusOK || downloadRec.Body.String() != previewData.Text {
			t.Fatalf("%s download = %d %q, want %q", label, downloadRec.Code, downloadRec.Body.String(), previewData.Text)
		}
	}

	assertHistoryPreviewAndDownload("before settings save")

	// Deferred settings transition: enable file logging while runtime is borrowed,
	// then complete activation via ConfigActivation status poll.
	gen1ID := backend.runtimeSnapshot().generationID
	gen1Logger := backend.currentLogger()
	borrowed, err := backend.borrowRuntime()
	if err != nil {
		t.Fatal(err)
	}
	releaseRuntime := sync.OnceFunc(borrowed.release)
	defer releaseRuntime()
	updated := backend.currentConfig()
	updated.Logging.FileEnabled = true
	updated.Logging.MaxTotalSizeMB = 1
	updated.Logging.MaxFiles = 2
	updated.Metadata.KeepImages = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := backend.SaveConfigActivation(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if activation.Status != api.ConfigActivationPending || backend.runtimeSnapshot().generationID != gen1ID {
		t.Fatalf("expected pending activation while borrowed, got %#v gen=%d", activation, backend.runtimeSnapshot().generationID)
	}
	releaseRuntime()
	activation, err = backend.ConfigActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gen2ID := backend.runtimeSnapshot().generationID
	gen2Logger := backend.currentLogger()
	if activation.Status != api.ConfigActivationActive || gen2ID == gen1ID || gen2Logger == gen1Logger {
		t.Fatalf("expected active replacement generation, got status=%s gen=%d->%d sameLogger=%t",
			activation.Status, gen1ID, gen2ID, gen2Logger == gen1Logger)
	}
	gen2Logger.SetConsoleOutput(io.Discard, io.Discard)

	assertHistoryPreviewAndDownload("after deferred file-logging activation")

	// Immediate effective settings transition also preserves memory-only input logs.
	updated2 := backend.currentConfig()
	updated2.Logging.Level = "debug"
	updated2.Metadata.OnlyID = true
	payload2, err := config.ExportToJSON(&updated2)
	if err != nil {
		t.Fatal(err)
	}
	activation2, err := backend.SaveConfigActivation(ctx, payload2)
	if err != nil {
		t.Fatal(err)
	}
	if activation2.Status != api.ConfigActivationActive || backend.runtimeSnapshot().generationID == gen2ID || backend.currentLogger() == gen2Logger {
		t.Fatalf("expected immediate replacement generation, got %#v gen=%d", activation2, backend.runtimeSnapshot().generationID)
	}
	backend.currentLogger().SetConsoleOutput(io.Discard, io.Discard)

	assertHistoryPreviewAndDownload("after immediate settings activation")

	// Restarting with a new Backend on the same database starts with an empty
	// memory store (no cross-process or global leakage).
	restartedCfg, err := config.LoadFromDatabase(ctx, backend.repo)
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyEnvOverrides(restartedCfg)
	restartedCfg.MainSettings.DBPath = dbPath
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	closedBackend = true
	restarted, err := NewBackendWithContext(ctx, *restartedCfg, newEventHub())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	attempts, err := restarted.repo.InputLogs(ctx, source, "")
	if err != nil || len(attempts) != 1 {
		t.Fatalf("restarted attempts = %#v, %v", attempts, err)
	}
	exported, err := logging.ReadInputLogs(ctx, restarted.currentLogger(), restarted.repo.DBPath(), attempts)
	if err != nil || exported.Coverage != "expired" || exported.Text != "" {
		t.Fatalf("restarted backend retained prior session memory logs: %#v, %v", exported, err)
	}
}

func TestPreReservationDeletePreservesFinishingRetryViaHistoryAndReadOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "pre-reservation-race.db")
	cfg := backendConfigTestConfig(dbPath)
	cfg.Logging.FileEnabled = true
	cfg.Logging.MaxTotalSizeMB = 1
	cfg.Logging.MaxFiles = 2
	cfg.Logging.Level = "warn"

	backend, err := NewBackendWithContext(ctx, cfg, newEventHub())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	logger := backend.currentLogger()
	logger.SetConsoleOutput(io.Discard, io.Discard)

	sessions, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessions.Close)
	active, err := sessions.Create("admin", false)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		backend:        backend,
		sessions:       sessions,
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	postJSON := func(route string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, route, strings.NewReader(string(raw)))
		req.Host = "127.0.0.1:7480"
		req.Header.Set("Origin", "http://127.0.0.1:7480")
		req.Header.Set("X-Csrf-Token", active.CSRFToken)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	getRoute := func(route string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, route, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: active.ID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	mediaRoot := t.TempDir()
	requested := filepath.Join(mediaRoot, "Requested", "Retry.Release.2026-GRP.mkv")
	verified := filepath.Join(mediaRoot, "Canonical", "Retry.Release.2026-GRP.mkv")
	unrelated := filepath.Join(mediaRoot, "Unrelated", "Keep.Release.2026-GRP.mkv")
	abandoned := filepath.Join(mediaRoot, "Abandoned", "Crashed.Release.2026-GRP.mkv")

	// Unrelated stored release + upload + log must remain preserved.
	if err := backend.repo.Save(ctx, db.FileMetadata{Path: unrelated, Title: "Keep Release"}); err != nil {
		t.Fatal(err)
	}
	if err := backend.repo.CreateUploadRecord(ctx, db.UploadRecord{
		Tracker:    "BLU",
		Status:     "uploaded",
		SourcePath: unrelated,
	}); err != nil {
		t.Fatal(err)
	}
	keepCtx, keepID, finishKeep := logger.BeginInputLog(ctx, backend.repo, unrelated, "preparation")
	logging.FromContext(keepCtx, logger).Infof("unrelated keep log")
	finishKeep("", "completed")

	// Earlier failed log-only History entry on requested path.
	oldCtx, oldID, finishOld := logger.BeginInputLog(ctx, backend.repo, requested, "open input")
	logging.FromContext(oldCtx, logger).Warnf("stale failed log-only attempt")
	finishOld("", "failed")

	// Abandoned attempt (started, never finished, no active slot).
	if err := backend.repo.BeginInputLog(ctx, db.InputLogAttempt{
		ID:            "0000000000000000000000ab",
		SourcePath:    abandoned,
		RequestedPath: abandoned,
		Kind:          "CLI input",
		StartedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Start a real CLI-style root capture with nested open-input phase before any
	// active slot is reserved, then delete the older History entry concurrently.
	retryCtx, retryID, finishRetry := logger.BeginInputLog(ctx, backend.repo, requested, "CLI input")
	nestedCtx, nestedID, bindVerified := logger.BeginInputLog(retryCtx, backend.repo, requested, "open input")
	if nestedID != retryID {
		t.Fatalf("nested capture ID = %q, want %q", nestedID, retryID)
	}
	logging.FromContext(nestedCtx, logger).Infof("active retry progress detail")

	if rec := postJSON("/api/app/DeleteInputLogs", struct{ SourcePath string }{requested}); rec.Code != http.StatusOK {
		t.Fatalf("pre-reservation DeleteInputLogs status = %d: %s", rec.Code, rec.Body.String())
	}

	// Complete nested verification rebinding and root capture after the delete.
	bindVerified(verified, "completed")
	finishRetry("", "completed")

	// History lists the rebound retry and unrelated stored release, not the old deleted path.
	listRec := getRoute("/api/app/ListInputHistory")
	var history inputHistoryList
	if err := json.Unmarshal(listRec.Body.Bytes(), &history); err != nil || listRec.Code != http.StatusOK {
		t.Fatalf("ListInputHistory = %d %s (%v)", listRec.Code, listRec.Body.String(), err)
	}
	seenVerified, seenRequested, seenUnrelated := false, false, false
	for _, entry := range history.Entries {
		switch entry.SourcePath {
		case verified:
			seenVerified = entry.LogsOnly
		case requested:
			seenRequested = true
		case unrelated:
			seenUnrelated = !entry.LogsOnly
		}
	}
	if !seenVerified || seenRequested || !seenUnrelated {
		t.Fatalf("unexpected history entries after retry finish: %#v", history.Entries)
	}

	// Preview, download, and read-only CLI lookup all find the new attempt and exclude the old one.
	previewRec := postJSON("/api/app/GetInputLog", struct{ SourcePath string }{verified})
	var preview struct {
		logging.InputLogExport
		DownloadID string
	}
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil || previewRec.Code != http.StatusOK ||
		preview.DownloadID != retryID || !strings.Contains(preview.Text, "active retry progress detail") ||
		strings.Contains(preview.Text, "stale failed log-only attempt") {
		t.Fatalf("GetInputLog(verified) = %d %s (%v)", previewRec.Code, previewRec.Body.String(), err)
	}
	if rec := getRoute("/api/app/InputLog/" + retryID); rec.Code != http.StatusOK || rec.Body.String() != preview.Text {
		t.Fatalf("InputLog(retryID) = %d %q", rec.Code, rec.Body.String())
	}
	if rec := getRoute("/api/app/InputLog/" + oldID); rec.Code != http.StatusNotFound {
		t.Fatalf("InputLog(oldID) = %d, want 404", rec.Code)
	}
	for _, query := range []string{requested, verified} {
		attempts, err := db.ReadOnlyInputLogs(ctx, dbPath, query)
		if err != nil || len(attempts) != 1 || attempts[0].ID != retryID ||
			attempts[0].SourcePath != verified || attempts[0].RequestedPath != requested || attempts[0].Outcome != "completed" {
			t.Fatalf("ReadOnlyInputLogs(%q) = %#v, %v", query, attempts, err)
		}
	}

	// Post-completion deletion stays deleted even if finish is called again.
	if rec := postJSON("/api/app/DeleteInputLogs", struct{ SourcePath string }{verified}); rec.Code != http.StatusOK {
		t.Fatalf("post-completion DeleteInputLogs status = %d: %s", rec.Code, rec.Body.String())
	}
	finishRetry("", "completed")
	if got, err := backend.repo.InputLogs(ctx, verified, ""); err != nil || len(got) != 0 {
		t.Fatalf("repeated finish resurrected deleted attempt: %#v, %v", got, err)
	}
	if got, err := db.ReadOnlyInputLogs(ctx, dbPath, requested); err != nil || len(got) != 0 {
		t.Fatalf("ReadOnlyInputLogs found deleted attempt after repeated finish: %#v, %v", got, err)
	}
	if rec := getRoute("/api/app/InputLog/" + retryID); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted retry download = %d, want 404", rec.Code)
	}

	// Abandoned history remains removable.
	if rec := postJSON("/api/app/DeleteInputLogs", struct{ SourcePath string }{abandoned}); rec.Code != http.StatusOK {
		t.Fatalf("abandoned DeleteInputLogs status = %d: %s", rec.Code, rec.Body.String())
	}
	if got, err := backend.repo.InputLogs(ctx, abandoned, ""); err != nil || len(got) != 0 {
		t.Fatalf("abandoned attempt was not removed: %#v, %v", got, err)
	}

	// Unrelated release metadata, uploads, and input logs remain untouched.
	if meta, err := backend.repo.GetByPath(ctx, unrelated); err != nil || meta.Title != "Keep Release" {
		t.Fatalf("unrelated metadata lost: %#v, %v", meta, err)
	}
	if uploads, err := backend.repo.ListUploadHistoryByPath(ctx, unrelated); err != nil || len(uploads) != 1 || uploads[0].Tracker != "BLU" {
		t.Fatalf("unrelated upload history lost: %#v, %v", uploads, err)
	}
	if keepLogs, err := backend.repo.InputLogs(ctx, unrelated, ""); err != nil || len(keepLogs) != 1 || keepLogs[0].ID != keepID {
		t.Fatalf("unrelated input logs lost: %#v, %v", keepLogs, err)
	}
}
