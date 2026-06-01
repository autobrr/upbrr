// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/services/db"

	"golang.org/x/crypto/argon2"
)

func newAuthTestServer(t *testing.T, dbPath string) *Server {
	t.Helper()

	repo, err := db.OpenWithLogger(dbPath, nil)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

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
		backend:        &Backend{repo: repo, hub: newEventHub()},
		auth:           auth,
		sessions:       sessions,
		authLimiter:    newFixedWindowLimiter(100, time.Minute),
		generalLimiter: newFixedWindowLimiter(100, time.Minute),
	}
}

func TestBootstrapRetainedSessionSetsPersistentCookie(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
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

func TestBootstrapRejectsRemoteFirstRunRequest(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "192.168.1.20:7480"
	req.RemoteAddr = "192.168.1.25:5000"

	recorder := httptest.NewRecorder()
	server.handleBootstrap(recorder, req, session{})

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected remote bootstrap to return 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "bootstrap is only available from localhost") {
		t.Fatalf("unexpected bootstrap rejection: %s", recorder.Body.String())
	}
}

func TestLoginUpgradesLegacyPasswordHash(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	password := "very-secure-password"
	salt := "legacy-salt-value"
	sum := argon2.IDKey(
		[]byte(password),
		[]byte(salt),
		legacyAuthArgon2Time,
		legacyAuthArgon2MemoryKB,
		legacyAuthArgon2Parallelism,
		legacyAuthArgon2KeyLen,
	)
	record := authRecord{
		Username:     "admin",
		PasswordHash: "argon2id$" + salt + "$" + base64.RawStdEncoding.EncodeToString(sum),
		CreatedAt:    time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(AuthFilePath(dbPath), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	body := `{"username":"admin","password":"very-secure-password","retainLogin":false}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"

	recorder := httptest.NewRecorder()
	server.handleLogin(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleLogin returned %d: %s", recorder.Code, recorder.Body.String())
	}

	updated, err := server.auth.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if updated.PasswordHash == record.PasswordHash {
		t.Fatal("expected legacy password hash to be upgraded after login")
	}
	if !strings.HasPrefix(updated.PasswordHash, "argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("expected upgraded hash format, got %q", updated.PasswordHash)
	}
	if strings.TrimSpace(updated.EncryptionKeySeed) == "" {
		t.Fatal("expected upgraded auth record to persist an encryption key seed")
	}
	if !verifyPassword(password, updated.PasswordHash) {
		t.Fatal("expected upgraded hash to verify")
	}
}

func TestLoginFinalizesPendingAuthUpgradeAfterInterruptedRewrap(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	password := "very-secure-password"
	salt := "legacy-salt-value"
	sum := argon2.IDKey(
		[]byte(password),
		[]byte(salt),
		legacyAuthArgon2Time,
		legacyAuthArgon2MemoryKB,
		legacyAuthArgon2Parallelism,
		legacyAuthArgon2KeyLen,
	)
	upgradedHash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	record := authRecord{
		Username:     "admin",
		PasswordHash: "argon2id$" + salt + "$" + base64.RawStdEncoding.EncodeToString(sum),
		CreatedAt:    time.Now().UTC(),
		PendingUpgrade: &authmaterial.PendingUpgrade{
			Stage: authmaterial.UpgradeStageDataRewrapped,
			Target: authRecord{
				Username:          "admin",
				PasswordHash:      upgradedHash,
				EncryptionKeySeed: "stable-seed-after-interrupt",
				CreatedAt:         time.Now().UTC(),
			},
			UpdatedAt: time.Now().UTC(),
		},
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(AuthFilePath(dbPath), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	body := `{"username":"admin","password":"very-secure-password","retainLogin":false}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"

	recorder := httptest.NewRecorder()
	server.handleLogin(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleLogin returned %d: %s", recorder.Code, recorder.Body.String())
	}

	updated, err := server.auth.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if updated.PendingUpgrade != nil {
		t.Fatal("expected interrupted upgrade marker to be cleared")
	}
	if updated.PasswordHash != upgradedHash {
		t.Fatal("expected login to finalize the upgraded password hash")
	}
	if updated.EncryptionKeySeed != "stable-seed-after-interrupt" {
		t.Fatalf("encryption seed = %q, want %q", updated.EncryptionKeySeed, "stable-seed-after-interrupt")
	}
	if !verifyPassword(password, updated.PasswordHash) {
		t.Fatal("expected finalized hash to verify")
	}
}

func TestBootstrapNonRetainedSessionDoesNotSetPersistentCookie(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":false}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
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
	bootstrapReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
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
	statusReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
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
	bootstrapReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
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
	statusReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
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

func TestAuthStatusMasksBrowseRootUntilAuthenticated(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	body := `{"username":"admin","password":"very-secure-password","retainLogin":false}`
	bootstrapReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	bootstrapReq.Header.Set("Content-Type", "application/json")
	bootstrapReq.Host = "127.0.0.1:7480"
	bootstrapReq.RemoteAddr = "127.0.0.1:5000"

	bootstrapRecorder := httptest.NewRecorder()
	server.handleBootstrap(bootstrapRecorder, bootstrapReq, session{})
	if bootstrapRecorder.Code != http.StatusOK {
		t.Fatalf("handleBootstrap returned %d: %s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}
	cookie := bootstrapRecorder.Result().Cookies()[0]

	record, err := server.auth.Load()
	if err != nil {
		t.Fatalf("load auth record: %v", err)
	}
	record.BrowseRoot = filepath.Join(t.TempDir(), "media")
	if err := server.auth.UpdateRecord(record); err != nil {
		t.Fatalf("update auth record: %v", err)
	}

	unauthReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
	unauthRecorder := httptest.NewRecorder()
	server.handleAuthStatus(unauthRecorder, unauthReq, session{})
	if unauthRecorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d: %s", unauthRecorder.Code, unauthRecorder.Body.String())
	}
	var unauthPayload struct {
		BrowseRoot string `json:"browseRoot"`
	}
	if err := json.Unmarshal(unauthRecorder.Body.Bytes(), &unauthPayload); err != nil {
		t.Fatalf("unmarshal unauth auth status: %v", err)
	}
	if unauthPayload.BrowseRoot != "" {
		t.Fatalf("expected unauthenticated auth status to mask browse root, got %q", unauthPayload.BrowseRoot)
	}

	authReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
	authReq.AddCookie(cookie)
	authRecorder := httptest.NewRecorder()
	server.handleAuthStatus(authRecorder, authReq, session{})
	if authRecorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d: %s", authRecorder.Code, authRecorder.Body.String())
	}
	var authPayload struct {
		BrowseRoot string `json:"browseRoot"`
	}
	if err := json.Unmarshal(authRecorder.Body.Bytes(), &authPayload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if authPayload.BrowseRoot != record.BrowseRoot {
		t.Fatalf("expected authenticated auth status to include browse root %q, got %q", record.BrowseRoot, authPayload.BrowseRoot)
	}
}

func TestDevelopmentNoAuthStatusBypassesMissingAuthOnLoopback(t *testing.T) {
	server := &Server{
		developmentNoAuth: true,
		developmentSession: session{
			ID:        "dev-no-auth",
			Username:  "dev",
			CSRFToken: "dev-csrf",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
		picker: &stubNativePicker{},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"

	recorder := httptest.NewRecorder()
	server.handleAuthStatus(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Authenticated           bool   `json:"authenticated"`
		NeedsSetup              bool   `json:"needsSetup"`
		Username                string `json:"username"`
		CSRFToken               string `json:"csrfToken"`
		AllowUnrestrictedBrowse bool   `json:"allowUnrestrictedBrowse"`
		NeedsBrowsePolicy       bool   `json:"needsBrowsePolicy"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !payload.Authenticated || payload.NeedsSetup || payload.Username != "dev" || payload.CSRFToken != "dev-csrf" {
		t.Fatalf("unexpected development auth status: %#v", payload)
	}
	if !payload.AllowUnrestrictedBrowse || payload.NeedsBrowsePolicy {
		t.Fatalf("expected development auth status to skip browse policy setup, got %#v", payload)
	}
}

func TestDevelopmentNoAuthStatusDoesNotBypassRemoteRequests(t *testing.T) {
	store, err := newAuthStore(filepath.Join(t.TempDir(), "state", "db.sqlite"))
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}
	server := &Server{
		auth:              store,
		developmentNoAuth: true,
		developmentSession: session{
			ID:        "dev-no-auth",
			Username:  "dev",
			CSRFToken: "dev-csrf",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/auth/status", nil)
	req.Host = "example.com:7480"
	req.RemoteAddr = "192.168.1.10:5000"

	recorder := httptest.NewRecorder()
	server.handleAuthStatus(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleAuthStatus returned %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Authenticated bool `json:"authenticated"`
		NeedsSetup    bool `json:"needsSetup"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.Authenticated || !payload.NeedsSetup {
		t.Fatalf("expected remote request to use normal auth status, got %#v", payload)
	}
}

func TestLogoutRemovesRetainedSessionFromDisk(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	current, err := server.sessions.Create("admin", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/logout", nil)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/logout", nil)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/BrowseFile", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:7480")
	req.Header.Set("X-Csrf-Token", current.CSRFToken)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected retained session to access app route after restart, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
