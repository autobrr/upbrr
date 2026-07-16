// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"

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

func TestBootstrapSessionCookieUsesConfiguredBasePath(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))
	server.cliCfg.BaseURL = "https://example.test/upbrr/"

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "example.test"
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
	if cookies[0].Path != "/upbrr" {
		t.Fatalf("cookie path = %q, want /upbrr", cookies[0].Path)
	}
}

func TestBootstrapAllowsRemoteFirstRunRequest(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "192.168.1.20:7480"
	req.RemoteAddr = "192.168.1.25:5000"

	recorder := httptest.NewRecorder()
	server.handleBootstrap(recorder, req, session{})

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected remote first-run bootstrap to return 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestBootstrapRejectsRemoteRequestWhenUserExists(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	body := `{"username":"admin","password":"very-secure-password","retainLogin":true}`

	first := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	first.Header.Set("Content-Type", "application/json")
	first.Host = "192.168.1.20:7480"
	first.RemoteAddr = "192.168.1.25:5000"

	firstRecorder := httptest.NewRecorder()
	server.handleBootstrap(firstRecorder, first, session{})
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("expected initial bootstrap to return 200, got %d: %s", firstRecorder.Code, firstRecorder.Body.String())
	}

	second := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/bootstrap", strings.NewReader(body))
	second.Header.Set("Content-Type", "application/json")
	second.Host = "192.168.1.20:7480"
	second.RemoteAddr = "192.168.1.25:5000"

	secondRecorder := httptest.NewRecorder()
	server.handleBootstrap(secondRecorder, second, session{})

	if secondRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected bootstrap after user exists to return 400, got %d: %s", secondRecorder.Code, secondRecorder.Body.String())
	}
	if !strings.Contains(secondRecorder.Body.String(), "user already exists") {
		t.Fatalf("unexpected bootstrap rejection: %s", secondRecorder.Body.String())
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

func TestRewrapProtectedDataForAuthChangeRecoversPreparedCookiesAfterPhasePersistFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)
	ctx := context.Background()

	oldRecord := authRecord{
		Username:     "admin",
		PasswordHash: "old-password-hash",
		CreatedAt:    time.Now().UTC(),
	}
	newRecord := authRecord{
		Username:          "admin",
		PasswordHash:      "new-password-hash",
		EncryptionKeySeed: "stable-seed-after-interrupt",
		CreatedAt:         oldRecord.CreatedAt,
	}
	oldRecord.PendingUpgrade = &authmaterial.PendingUpgrade{
		Stage:     authmaterial.UpgradeStagePrepared,
		Target:    newRecord,
		UpdatedAt: time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(oldRecord, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(AuthFilePath(dbPath), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rawDB := server.backend.repo.RawDB()
	oldKey, err := cookies.NewKeyManager(rawDB).InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		t.Fatalf("initialize old cookie key: %v", err)
	}
	store, err := cookies.NewCookieStore(rawDB)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}
	if err := store.SaveCookie(ctx, "tracker", "session", "cookie-value", oldKey); err != nil {
		t.Fatalf("save old cookie: %v", err)
	}
	if err := cookies.RewrapCookiesWithAuthChange(ctx, rawDB, oldRecord.AuthMaterial(), newRecord.AuthMaterial()); err != nil {
		t.Fatalf("simulate cookie rewrap before phase persistence: %v", err)
	}

	if err := server.rewrapProtectedDataForAuthChange(ctx, oldRecord, newRecord); err != nil {
		t.Fatalf("retry prepared auth rewrap: %v", err)
	}

	updated, err := server.auth.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if updated.PendingUpgrade == nil {
		t.Fatal("expected pending upgrade to remain until login finalizes it")
	}
	if updated.PendingUpgrade.Stage != authmaterial.UpgradeStageDataRewrapped {
		t.Fatalf("pending stage = %q, want %q", updated.PendingUpgrade.Stage, authmaterial.UpgradeStageDataRewrapped)
	}

	newHelper, _, err := newRecord.AuthMaterial().PrimaryHelper()
	if err != nil {
		t.Fatalf("new auth helper: %v", err)
	}
	salt, err := loadCookieEncryptionSalt(ctx, rawDB)
	if err != nil {
		t.Fatalf("load cookie salt: %v", err)
	}
	newKey, err := cookies.DeriveEncryptionKey(newHelper, salt)
	if err != nil {
		t.Fatalf("derive new cookie key: %v", err)
	}
	value, err := store.GetCookie(ctx, "tracker", "session", newKey)
	if err != nil {
		t.Fatalf("decrypt recovered cookie with new key: %v", err)
	}
	if value != "cookie-value" {
		t.Fatalf("recovered cookie = %q, want %q", value, "cookie-value")
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

func TestLogoutClearsConfiguredBasePathCookie(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)
	server.cliCfg.BaseURL = "/upbrr/"

	current, err := server.sessions.Create("admin", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/logout", nil)
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	recorder := httptest.NewRecorder()

	server.handleLogout(recorder, req, current)

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleLogout returned %d: %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one clear cookie, got %d", len(cookies))
	}
	if cookies[0].Path != "/upbrr" || cookies[0].MaxAge != -1 {
		t.Fatalf("unexpected clear cookie: %#v", cookies[0])
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

func TestLogoutStopsSessionLogStreams(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	stream := &backendLogStream{
		id:        "stream-1",
		sessionID: current.ID,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	server.backend.streams = map[string]*backendLogStream{stream.id: stream}
	server.backend.streamWG.Go(func() {
		<-stream.stop
		close(stream.done)
	})
	t.Cleanup(func() {
		server.backend.StopSessionLogStreams(current.ID)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/logout", nil)
	req.Host = "127.0.0.1:7480"
	req.RemoteAddr = "127.0.0.1:5000"
	recorder := httptest.NewRecorder()

	server.handleLogout(recorder, req, current)

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleLogout returned %d: %s", recorder.Code, recorder.Body.String())
	}

	select {
	case <-stream.done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected logout to stop active session log streams")
	}

	server.backend.streamMu.Lock()
	_, ok := server.backend.streams[stream.id]
	server.backend.streamMu.Unlock()
	if ok {
		t.Fatal("expected logout to remove session log stream")
	}
}

func TestLogoutRejectsMismatchedCSRFAndCookieSession(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	first, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/auth/logout", nil)
	req.Host = "127.0.0.1:7480"
	req.Header.Set("Origin", "http://127.0.0.1:7480")
	req.Header.Set("X-Csrf-Token", first.CSRFToken)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: second.ID})
	recorder := httptest.NewRecorder()

	server.requireSession(server.handleLogout)(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized logout, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, ok := server.sessions.Get(first.ID); !ok {
		t.Fatal("expected first session to remain active")
	}
	if _, ok := server.sessions.Get(second.ID); !ok {
		t.Fatal("expected second session to remain active")
	}
}

func TestRetainedSessionCanAccessAppRouteAfterRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	server := newAuthTestServer(t, dbPath)

	current, err := server.sessions.Create("admin", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	server.sessions.Close()

	reloaded := newAuthTestServer(t, dbPath)

	mux := http.NewServeMux()
	reloaded.registerAppRoutes(mux)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/GetApplicationInfo", strings.NewReader(`{}`))
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

func TestTrackerAuthBackendUsesRequestContext(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<input name="token" value="abcdefghijklmnop">authkey=abcdefghijklmnopqrstuvwxyzABCDEF`))
	}))
	t.Cleanup(trackerServer.Close)

	backend := &Backend{
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {
						URL:      trackerServer.URL,
						Username: "user",
						Password: "pass",
					},
				},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status, err := backend.TestTrackerAuth(ctx, "MTV")
	if err != nil {
		t.Fatalf("TestTrackerAuth: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("expected canceled context to prevent remote auth request, got %d request(s)", requests.Load())
	}
	if !strings.Contains(status.LastError, "context canceled") {
		t.Fatalf("expected context canceled status, got %#v", status)
	}
}

func TestTrackerAuthBackendLoginBTNCookiesWithoutAPIPreservesMissingAPIStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newTrackerAuthWebTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	backend := &Backend{cfg: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}}

	status, err := backend.LoginTrackerAuth(ctx, "BTN", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("LoginTrackerAuth: %v", err)
	}
	if status.State != "login_required" || status.CookieCount != 1 || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected BTN cookie-only web login to preserve missing API status, got %#v", status)
	}
	if strings.Contains(status.Message, "username/password missing") {
		t.Fatalf("missing credentials masked missing API status: %#v", status)
	}
}

func TestTrackerAuthImportCanceledContextDoesNotPersistCookies(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthWebTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	backend := &Backend{cfg: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := backend.ImportTrackerAuthCookieContent(ctx, "AR", "cookies.txt", ".example.test\tTRUE\t/\tTRUE\t0\tsession\tabc\n")
	if err == nil {
		t.Fatal("expected canceled import error")
	}
	if _, loadErr := cookies.LoadTrackerCookieMap(context.Background(), dbPath, "AR"); loadErr == nil {
		t.Fatal("canceled import persisted cookies")
	}
}

func TestTrackerAuthDeleteCanceledContextDoesNotDeleteCookies(t *testing.T) {
	t.Parallel()

	dbPath := newTrackerAuthWebTestDB(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := cookies.SaveTrackerCookieMap(context.Background(), dbPath, "AR", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	backend := &Backend{cfg: config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := backend.DeleteTrackerAuth(ctx, "AR")
	if err == nil {
		t.Fatal("expected canceled delete error")
	}
	values, loadErr := cookies.LoadTrackerCookieMap(context.Background(), dbPath, "AR")
	if loadErr != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", loadErr)
	}
	if values["session"] != "abc" {
		t.Fatal("canceled delete changed cookies")
	}
}

func newTrackerAuthWebTestDB(t *testing.T) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close repo: %v", err)
	}
	return dbPath
}

func TestRequestSessionTokenMustMatchCookieSession(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	first, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/StartDupeCheck", strings.NewReader(`{}`))
	req.Header.Set("X-Csrf-Token", first.CSRFToken)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: second.ID})

	if current, ok := server.currentSession(req); ok {
		t.Fatalf("expected mismatched request token to reject cookie session, got %#v", current)
	}
}

func TestInvalidRequestSessionTokenDoesNotFallbackToCookie(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/StartDupeCheck", strings.NewReader(`{}`))
	req.Header.Set("X-Csrf-Token", "not-current-token")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})

	if resolved, ok := server.currentSession(req); ok {
		t.Fatalf("expected invalid request token to reject cookie fallback, got %#v", resolved)
	}
}

func TestQuerySessionTokenDoesNotAuthenticateAppRoute(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/GetConfig?csrfToken="+current.CSRFToken, nil)
	if resolved, ok := server.currentSession(req); ok {
		t.Fatalf("expected query token without cookie to reject app route, got %#v", resolved)
	}
}

func TestEventHeaderSessionTokenMustMatchCookieSession(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	first, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/events", nil)
	req.Header.Set("X-Csrf-Token", first.CSRFToken)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: second.ID})

	if current, ok := server.currentSession(req); ok {
		t.Fatalf("expected mismatched event token to reject cookie session, got %#v", current)
	}
}

func TestEventHeaderSessionTokenWithoutCookieDoesNotAuthenticate(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/events", nil)
	req.Header.Set("X-Csrf-Token", current.CSRFToken)

	if resolved, ok := server.currentSession(req); ok {
		t.Fatalf("expected header token without cookie to reject event route, got %#v", resolved)
	}
}

func TestEventQuerySessionTokenDoesNotOverrideCookie(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	first, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/events?csrfToken="+first.CSRFToken, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: second.ID})

	current, ok := server.currentSession(req)
	if !ok {
		t.Fatal("expected cookie session to resolve")
	}
	if current.ID != second.ID {
		t.Fatalf("expected query token not to override cookie session, got %q", current.ID)
	}
}

func TestCancelDupeCheckRequiresPost(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	canceled := make(chan struct{}, 1)
	release := make(chan struct{})
	jobID := installBlockingDupeJob(t, server.backend, current.ID, canceled, release)
	t.Cleanup(func() { close(release) })
	body := fmt.Sprintf(`{"JobID":%q}`, jobID)

	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	getReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/CancelDupeCheck", strings.NewReader(body))
	getReq.Header.Set("Content-Type", "application/json")
	getReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	getRecorder := httptest.NewRecorder()

	mux.ServeHTTP(getRecorder, getReq)

	if getRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected GET cancel to be rejected, got %d: %s", getRecorder.Code, getRecorder.Body.String())
	}
	select {
	case <-canceled:
		t.Fatal("expected GET cancel request to leave dupe job running")
	default:
	}

	postReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/CancelDupeCheck", strings.NewReader(body))
	postReq.Host = "127.0.0.1:7480"
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Origin", "http://127.0.0.1:7480")
	postReq.Header.Set("X-Csrf-Token", current.CSRFToken)
	postReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	postRecorder := httptest.NewRecorder()

	mux.ServeHTTP(postRecorder, postReq)

	if postRecorder.Code != http.StatusOK {
		t.Fatalf("expected POST cancel to succeed, got %d: %s", postRecorder.Code, postRecorder.Body.String())
	}
	select {
	case <-canceled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected POST cancel request to cancel dupe job")
	}
}

func TestCancelTrackerUploadRequiresPost(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	canceled := make(chan struct{}, 1)
	release := make(chan struct{})
	jobID := installBlockingUploadJob(t, server.backend, current.ID, canceled, release)
	t.Cleanup(func() { close(release) })
	body := fmt.Sprintf(`{"JobID":%q}`, jobID)

	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	getReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/CancelTrackerUpload", strings.NewReader(body))
	getReq.Header.Set("Content-Type", "application/json")
	getReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	getRecorder := httptest.NewRecorder()

	mux.ServeHTTP(getRecorder, getReq)

	if getRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected GET cancel to be rejected, got %d: %s", getRecorder.Code, getRecorder.Body.String())
	}
	select {
	case <-canceled:
		t.Fatal("expected GET cancel request to leave tracker upload job running")
	default:
	}

	postReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/app/CancelTrackerUpload", strings.NewReader(body))
	postReq.Host = "127.0.0.1:7480"
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Origin", "http://127.0.0.1:7480")
	postReq.Header.Set("X-Csrf-Token", current.CSRFToken)
	postReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	postRecorder := httptest.NewRecorder()

	mux.ServeHTTP(postRecorder, postReq)

	if postRecorder.Code != http.StatusOK {
		t.Fatalf("expected POST cancel to succeed, got %d: %s", postRecorder.Code, postRecorder.Body.String())
	}
	select {
	case <-canceled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected POST cancel request to cancel tracker upload job")
	}
}

func TestStopSessionLogStreamsIfIdleLatestDisconnectWins(t *testing.T) {
	backend := &Backend{
		streams: make(map[string]*backendLogStream),
	}
	stream := &backendLogStream{
		id:        "stream-1",
		sessionID: "session-a",
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	backend.streams[stream.id] = stream
	backend.streamWG.Go(func() {
		<-stream.stop
		close(stream.done)
	})
	t.Cleanup(func() {
		_ = backend.StopLogStream("session-a", stream.id)
	})

	hub := newEventHub()
	server := &Server{
		backend:        backend,
		hub:            hub,
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
	}
	t.Cleanup(func() {
		clearSessionLogStopGeneration(server, "session-a")
	})

	server.stopSessionLogStreamsIfIdle("session-a")

	hub.mu.Lock()
	time.Sleep(eventSessionLogStopGracePeriod + eventSessionLogStopGracePeriod/2)
	server.stopSessionLogStreamsIfIdle("session-a")
	hub.mu.Unlock()

	deadline := time.Now().Add(250 * time.Millisecond)
	for testSessionALogStopGeneration(t, server) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("expected latest disconnect to own generation 2, got %d", testSessionALogStopGeneration(t, server))
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-stream.done:
		t.Fatal("expected stale disconnect timer to leave session log streams active during newer grace window")
	default:
	}

	backend.streamMu.Lock()
	_, streamStillRegistered := backend.streams[stream.id]
	backend.streamMu.Unlock()
	if !streamStillRegistered {
		t.Fatal("expected stale disconnect timer to leave session log streams registered during newer grace window")
	}

	select {
	case <-stream.done:
	case <-time.After(eventSessionLogStopGracePeriod + 150*time.Millisecond):
		t.Fatal("expected latest disconnect grace timer to stop session log streams")
	}
}

func TestStopSessionLogStreamsIfIdleSubscriberAtStopBoundaryKeepsStreamActiveAndClearsGeneration(t *testing.T) {
	backend := &Backend{
		streams: make(map[string]*backendLogStream),
	}
	stream := &backendLogStream{
		id:        "stream-1",
		sessionID: "session-a",
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	backend.streams[stream.id] = stream
	backend.streamWG.Go(func() {
		<-stream.stop
		close(stream.done)
	})

	hub := newEventHub()
	server := &Server{
		backend:        backend,
		hub:            hub,
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
	}
	t.Cleanup(func() {
		hub.mu.Lock()
		if subs, ok := hub.subscribers["session-a"]; ok {
			for ch := range subs {
				delete(subs, ch)
				close(ch)
			}
			delete(hub.subscribers, "session-a")
		}
		hub.mu.Unlock()
		clearSessionLogStopGeneration(server, "session-a")
		_ = backend.StopLogStream("session-a", stream.id)
	})

	server.stopSessionLogStreamsIfIdle("session-a")

	hub.mu.Lock()
	time.Sleep(eventSessionLogStopGracePeriod + eventSessionLogStopGracePeriod/2)
	reconnected := make(chan serverEvent)
	hub.subscribers["session-a"] = map[chan serverEvent]struct{}{reconnected: {}}
	hub.mu.Unlock()

	deadline := time.Now().Add(250 * time.Millisecond)
	for testSessionALogStopGeneration(t, server) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("expected active replacement subscriber to clear idle-stop generation, got %d", testSessionALogStopGeneration(t, server))
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-stream.done:
		t.Fatal("expected replacement subscriber at stop boundary to keep session log streams active")
	default:
	}

	backend.streamMu.Lock()
	_, streamStillRegistered := backend.streams[stream.id]
	backend.streamMu.Unlock()
	if !streamStillRegistered {
		t.Fatal("expected replacement subscriber at stop boundary to keep session log streams registered")
	}
}

func testSessionALogStopGeneration(t *testing.T, s *Server) uint64 {
	t.Helper()

	sessionLogStopGenerations.mu.Lock()
	defer sessionLogStopGenerations.mu.Unlock()

	return sessionLogStopGenerations.byServ[s]["session-a"]
}

func TestSaveConfigRejectsGet(t *testing.T) {
	server := newAuthTestServer(t, filepath.Join(t.TempDir(), "state", "db.sqlite"))

	current, err := server.sessions.Create("admin", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	server.registerAppRoutes(mux)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/app/SaveConfig", strings.NewReader(`{"Payload":"{}"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: current.ID})
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
