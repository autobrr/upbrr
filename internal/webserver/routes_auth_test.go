// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newAuthTestServer(t *testing.T, dbPath string) *Server {
	t.Helper()

	auth, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}
	sessions, err := newSessionManager(60, dbPath)
	if err != nil {
		t.Fatalf("newSessionManager: %v", err)
	}
	t.Cleanup(func() {
		sessions.Close()
	})

	return &Server{
		auth:           auth,
		sessions:       sessions,
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
}

func TestBootstrapRetainedSessionSetsPersistentCookie(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"

	recorder := httptest.NewRecorder()
	server.handleBootstrap(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleBootstrap returned %d: %s", recorder.Code, recorder.Body.String())
	}

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName {
		t.Fatalf("cookie name = %q, want %q", cookie.Name, sessionCookieName)
	}
	if cookie.MaxAge <= 0 {
		t.Fatalf("expected persistent cookie MaxAge > 0, got %d", cookie.MaxAge)
	}
	if cookie.Expires.IsZero() {
		t.Fatal("expected persistent cookie expiry to be set")
	}
}

func TestBootstrapNonRetainedSessionDoesNotSetPersistentCookie(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"

	recorder := httptest.NewRecorder()
	server.handleBootstrap(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleBootstrap returned %d: %s", recorder.Code, recorder.Body.String())
	}

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.MaxAge != 0 {
		t.Fatalf("expected session cookie MaxAge = 0, got %d", cookie.MaxAge)
	}
	if !cookie.Expires.IsZero() {
		t.Fatalf("expected session cookie expiry to be empty, got %s", cookie.Expires)
	}
}

func TestAuthStatusRestoresRetainedSessionAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`
	bootstrapReq := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	bootstrapReq.Header.Set("Content-Type", "application/json")
	bootstrapReq.Host = "127.0.0.1:7480"
	bootstrapReq.RemoteAddr = "127.0.0.1:5000"

	bootstrapRecorder := httptest.NewRecorder()
	server.handleBootstrap(bootstrapRecorder, bootstrapReq, session{})
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("handleBootstrap returned %d: %s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}

	cookie := bootstrapRecorder.Result().Cookies()[0]
	server.sessions.Close()

	reloaded := newAuthTestServer(t, dbPath)
	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	statusReq.Host = "127.0.0.1:7480"
	statusReq.RemoteAddr = "127.0.0.1:5000"
	statusReq.AddCookie(cookie)

	statusRecorder := httptest.NewRecorder()
	reloaded.handleAuthStatus(statusRecorder, statusReq, session{})

	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	if !strings.Contains(statusRecorder.Body.String(), `"authenticated":true`) {
		t.Fatalf("expected restored retained session to authenticate, got %s", statusRecorder.Body.String())
	}
}

func TestAuthStatusRejectsNonRetainedSessionAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	body := `{"username":"admin","password":"very-secure-password","retainLogin":false}`
	bootstrapReq := httptest.NewRequest(http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	bootstrapReq.Header.Set("Content-Type", "application/json")
	bootstrapReq.Host = "127.0.0.1:7480"
	bootstrapReq.RemoteAddr = "127.0.0.1:5000"

	bootstrapRecorder := httptest.NewRecorder()
	server.handleBootstrap(bootstrapRecorder, bootstrapReq, session{})
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("handleBootstrap returned %d: %s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}

	cookie := bootstrapRecorder.Result().Cookies()[0]
	server.sessions.Close()

	reloaded := newAuthTestServer(t, dbPath)
	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	statusReq.Host = "127.0.0.1:7480"
	statusReq.RemoteAddr = "127.0.0.1:5000"
	statusReq.AddCookie(cookie)

	statusRecorder := httptest.NewRecorder()
	reloaded.handleAuthStatus(statusRecorder, statusReq, session{})

	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	if !strings.Contains(statusRecorder.Body.String(), `"authenticated":false`) {
		t.Fatalf("expected non-retained session to be lost after restart, got %s", statusRecorder.Body.String())
	}
}

func TestLogoutRemovesRetainedSessionFromDisk(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	current, err := server.sessions.Create("admin", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	recorder := httptest.NewRecorder()

	server.handleLogout(recorder, req, current)

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleLogout returned %d: %s", recorder.Code, recorder.Body.String())
	}

	server.sessions.Close()

	reloaded := newAuthTestServer(t, dbPath)
	if _, ok := reloaded.sessions.Get(current.ID); ok {
		t.Fatal("expected logout to remove retained session from disk")
	}
}

func TestLogoutReturnsErrorWhenRetainedSessionPersistenceFails(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	current, err := server.sessions.Create("admin", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	blockedPath := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	server.sessions.store.path = blockedPath

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	recorder := httptest.NewRecorder()

	server.handleLogout(recorder, req, current)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected logout failure status, got %d", recorder.Code)
	}
	if _, ok := server.sessions.Get(current.ID); !ok {
		t.Fatal("expected session to remain active when logout persistence fails")
	}
}

func TestRetainedSessionCanAccessAppRouteAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)
	server.picker = &stubNativePicker{filePath: `C:\Media\movie.mkv`}

	current, err := server.sessions.Create("admin", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	server.sessions.Close()

	reloaded := newAuthTestServer(t, dbPath)
	reloaded.picker = &stubNativePicker{filePath: `C:\Media\movie.mkv`}

	mux := http.NewServeMux()
	reloaded.registerAppRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/app/BrowseFile", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:7480")
	req.Header.Set("X-CSRF-Token", current.CSRFToken)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected retained session to access app route after restart, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
