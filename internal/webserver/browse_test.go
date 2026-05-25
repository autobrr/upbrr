// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type stubNativePicker struct {
	filePath    string
	folderPath  string
	fileErr     error
	folderErr   error
	fileCalls   int
	folderCalls int
}

func (s *stubNativePicker) BrowseFile() (string, error) {
	s.fileCalls++
	return s.filePath, s.fileErr
}

func (s *stubNativePicker) BrowseFolder() (string, error) {
	s.folderCalls++
	return s.folderPath, s.folderErr
}

func testSessionManager() *sessionManager {
	return &sessionManager{
		ttl:      time.Hour,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		sessions: map[string]session{},
	}
}

func testServerWithPicker(picker nativePicker) *Server {
	manager := testSessionManager()
	manager.sessions["test-session"] = session{
		ID:        "test-session",
		Username:  "tester",
		CSRFToken: "test-csrf",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	return &Server{
		picker:         picker,
		sessions:       manager,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
	}
}

func testServerWithBackend(t *testing.T, repo *db.SQLiteRepository, cfg config.Config) *Server {
	t.Helper()
	manager := testSessionManager()
	manager.sessions["test-session"] = session{
		ID:        "test-session",
		Username:  "tester",
		CSRFToken: "test-csrf",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	return &Server{
		backend:        &Backend{repo: repo, cfg: cfg},
		sessions:       manager,
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
	}
}

func openBrowseTestRepo(t *testing.T) (*db.SQLiteRepository, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	repo, err := db.OpenWithLogger(dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}
	return repo, dbPath
}

func setTestBrowsePolicy(t *testing.T, server *Server, dbPath string, root string, allowUnrestricted bool) {
	t.Helper()
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("new auth store: %v", err)
	}
	if err := store.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatalf("load auth: %v", err)
	}
	record.BrowseRoot = root
	record.AllowUnrestrictedBrowse = allowUnrestricted
	if err := store.UpdateRecord(record); err != nil {
		t.Fatalf("update auth: %v", err)
	}
	server.auth = store
}

func newBrowseRequest(path string, host string, remoteAddr string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(`{}`))
	req.Host = host
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+host)
	req.Header.Set("X-CSRF-Token", "test-csrf")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	return req
}

func TestIsLoopbackHostname(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "sub.localhost", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "192.168.1.20", want: false},
		{host: "example.com", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isLoopbackHostname(tc.host); got != tc.want {
				t.Fatalf("isLoopbackHostname(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestHandleAuthStatusIncludesNativeBrowseCapability(t *testing.T) {
	store, err := newAuthStore(filepath.Join(t.TempDir(), "state", "db.sqlite"))
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}
	server := &Server{
		auth:   store,
		picker: &stubNativePicker{},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = "127.0.0.1:5050"

	recorder := httptest.NewRecorder()
	server.handleAuthStatus(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d", recorder.Code)
	}

	var payload struct {
		NativeBrowseEnabled bool `json:"nativeBrowseEnabled"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !payload.NativeBrowseEnabled {
		t.Fatal("expected localhost auth status to advertise native browse support")
	}
}

func TestBrowseFileRouteAllowsLocalhostSessions(t *testing.T) {
	picker := &stubNativePicker{filePath: `C:\Media\movie.mkv`}
	server := testServerWithPicker(picker)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	recorder := httptest.NewRecorder()
	req := newBrowseRequest("/api/app/BrowseFile", "127.0.0.1:8080", "127.0.0.1:5050")
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("browse file route returned %d", recorder.Code)
	}
	if picker.fileCalls != 1 {
		t.Fatalf("expected picker to be called once, got %d", picker.fileCalls)
	}
	if got := strings.TrimSpace(recorder.Body.String()); !strings.Contains(got, `C:\\Media\\movie.mkv`) {
		t.Fatalf("expected response to include selected path, got %q", got)
	}
}

func TestBrowseFileRouteRejectsRemoteSessions(t *testing.T) {
	picker := &stubNativePicker{filePath: `C:\Media\movie.mkv`}
	server := testServerWithPicker(picker)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/BrowseFile", "example.com:8080", "192.168.1.25:5050")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden status, got %d", recorder.Code)
	}
	if picker.fileCalls != 0 {
		t.Fatalf("expected picker not to be called, got %d calls", picker.fileCalls)
	}
	if !strings.Contains(recorder.Body.String(), "localhost web sessions") {
		t.Fatalf("expected remote browse error message, got %q", recorder.Body.String())
	}
}

func TestUIStateRoutePersistsSharedState(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	post := newBrowseRequest("/api/app/UIState", "example.com:8080", "192.168.1.25:5050")
	post.Body = io.NopCloser(strings.NewReader(`{"id":"state-a","label":"Dupes","state":{"path":"/media/release","activeTab":"dupes"}}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save ui state returned %d: %s", recorder.Code, recorder.Body.String())
	}

	get := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/UIState?id=state-a", nil)
	get.Host = "example.com:8080"
	get.RemoteAddr = "192.168.1.25:5050"
	get.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get ui state returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.UIStateRecord
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal ui state: %v", err)
	}
	if payload.ID != "state-a" || payload.Label != "Dupes" || payload.State["path"] != "/media/release" || payload.State["activeTab"] != "dupes" {
		t.Fatalf("unexpected ui state payload: %#v", payload)
	}

	post = newBrowseRequest("/api/app/UIState", "example.com:8080", "192.168.1.25:5050")
	post.Body = io.NopCloser(strings.NewReader(`{"id":"state-b","label":"Upload","state":{"path":"/media/other","activeTab":"upload"}}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, post)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save second ui state returned %d: %s", recorder.Code, recorder.Body.String())
	}

	list := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/UIState", nil)
	list.Host = "example.com:8080"
	list.RemoteAddr = "192.168.1.25:5050"
	list.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, list)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list ui states returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var stateList api.UIStateList
	if err := json.Unmarshal(recorder.Body.Bytes(), &stateList); err != nil {
		t.Fatalf("unmarshal ui state list: %v", err)
	}
	seen := map[string]bool{}
	for _, record := range stateList.States {
		seen[record.ID] = true
	}
	if len(stateList.States) != 2 || !seen["state-a"] || !seen["state-b"] {
		t.Fatalf("expected both live ui states, got %#v", stateList.States)
	}
	if stateList.States[0].ID != "state-a" || stateList.States[1].ID != "state-b" {
		t.Fatalf("expected stable live ui state order, got %#v", stateList.States)
	}
}

func TestUIStateRouteRejectsUnauthenticatedAndBadCSRF(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	cases := []struct {
		name           string
		request        func() *http.Request
		expectedStatus int
	}{
		{
			name: "unauthenticated",
			request: func() *http.Request {
				return httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/UIState", nil)
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "bad csrf",
			request: func() *http.Request {
				req := newBrowseRequest("/api/app/UIState", "example.com:8080", "192.168.1.25:5050")
				req.Header.Del("X-CSRF-Token")
				return req
			},
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, tc.request())
			if recorder.Code != tc.expectedStatus {
				t.Fatalf("expected status %d, got %d", tc.expectedStatus, recorder.Code)
			}
		})
	}
}

func TestUIStateRouteRejectsBlankPostID(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/UIState", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"id":"   ","label":"Blank","state":{}}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected blank id to return 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "id is required") {
		t.Fatalf("expected id validation error, got %s", recorder.Body.String())
	}
}

func TestBrowseDirectoryRouteAllowsRemoteSessionsAndSortsEntries(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "b-folder"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c-video.mkv"), []byte("video"), 0o600); err != nil {
		t.Fatalf("write video: %v", err)
	}
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(root) + `,"mode":"file"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse directory returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browse directory: %v", err)
	}
	if len(payload.Entries) < 2 {
		t.Fatalf("expected at least two entries, got %#v", payload.Entries)
	}
	if payload.Entries[0].Name != "b-folder" || !payload.Entries[0].IsDir {
		t.Fatalf("expected folder first, got %#v", payload.Entries)
	}
	if payload.Entries[1].Name != "c-video.mkv" || payload.Entries[1].IsDir {
		t.Fatalf("expected file second, got %#v", payload.Entries)
	}
	for _, entry := range payload.Entries {
		if entry.Name == "a-file.txt" {
			t.Fatalf("expected non-video file to be hidden, got %#v", payload.Entries)
		}
	}
}

func TestBrowseDirectoryRouteRequiresWebBrowsePolicy(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	root := t.TempDir()
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("new auth store: %v", err)
	}
	if err := store.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	server.auth = store
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(root) + `,"mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected missing browse policy to return 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "web browse root is not configured") {
		t.Fatalf("expected browse policy error, got %s", recorder.Body.String())
	}
}

func TestBrowseDirectoryRouteHonorsWebAuthBrowseRoot(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(allowed, 0o755); err != nil {
		t.Fatalf("mkdir allowed: %v", err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, root, false)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":"","mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse root returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browse root: %v", err)
	}
	if payload.CurrentPath != root {
		t.Fatalf("expected constrained root %q, got %q", root, payload.CurrentPath)
	}
	if payload.ParentPath != "" {
		t.Fatalf("expected no parent above constrained root, got %q", payload.ParentPath)
	}

	req = newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(outside) + `,"mode":"folder"}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected outside browse root to return 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "outside configured web browse root") {
		t.Fatalf("expected browse root error, got %s", recorder.Body.String())
	}
}

func TestBrowseDirectoryRouteHonorsMultipleWebAuthBrowseRoots(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, dir := range []string{first, second, outside} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, first+", "+second, false)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":"","mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse roots returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload api.BrowseDirectoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browse roots: %v", err)
	}
	if payload.CurrentPath != "" || len(payload.Entries) != 2 {
		t.Fatalf("expected virtual root with two entries, got %#v", payload)
	}
	if payload.Entries[0].Path != first || payload.Entries[1].Path != second {
		t.Fatalf("expected both configured roots, got %#v", payload.Entries)
	}

	req = newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(second) + `,"mode":"folder"}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browse second root returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal second root: %v", err)
	}
	if payload.CurrentPath != second || payload.ParentPath != "" {
		t.Fatalf("expected constrained second root, got %#v", payload)
	}

	req = newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(outside) + `,"mode":"folder"}`))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected outside browse roots to return 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestBrowseDirectoryRouteRejectsInvalidPath(t *testing.T) {
	repo, dbPath := openBrowseTestRepo(t)
	server := testServerWithBackend(t, repo, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
	})
	setTestBrowsePolicy(t, server, dbPath, "", true)
	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := newBrowseRequest("/api/app/BrowseDirectory", "example.com:8080", "192.168.1.25:5050")
	req.Body = io.NopCloser(strings.NewReader(`{"path":` + strconv.Quote(filepath.Join(t.TempDir(), "missing")) + `,"mode":"folder"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid path to return 400, got %d", recorder.Code)
	}
}
