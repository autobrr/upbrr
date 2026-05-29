// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

func TestAuthStateMarshalOmitsHelper(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(authState{Fingerprint: "fingerprint-123"})
	if err != nil {
		t.Fatalf("marshal auth state: %v", err)
	}

	got := string(payload)
	if strings.Contains(got, "helper") {
		t.Fatalf("expected auth state JSON to omit helper, got %s", got)
	}
	if !strings.Contains(got, `"fingerprint":"fingerprint-123"`) {
		t.Fatalf("expected fingerprint in auth state JSON, got %s", got)
	}
}

func TestGetAuthStateFromDBToleratesLegacyHelper(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		cookieAuthStateConfigSection, `{"helper":"legacy-secret","fingerprint":"legacy-fingerprint"}`); err != nil {
		t.Fatalf("insert legacy auth state: %v", err)
	}

	state, err := getAuthStateFromDB(ctx, db)
	if err != nil {
		t.Fatalf("get auth state: %v", err)
	}

	if state.Fingerprint != "legacy-fingerprint" {
		t.Fatalf("expected fingerprint to round-trip, got %q", state.Fingerprint)
	}
	if !state.legacyHelperFound {
		t.Fatalf("expected legacy helper to be detected")
	}
}

func TestNewKeyManagerPanicsOnNilDB(t *testing.T) {
	t.Parallel()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic for nil db")
		}
		if recovered != "nil db passed to NewKeyManager" {
			t.Fatalf("unexpected panic value: %v", recovered)
		}
	}()

	_ = NewKeyManager(nil)
}

func TestInitializeEncryptionKeyStoresFingerprintOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := newTestCookieDB(t)
	dbPath := writeWebAuthFile(t, "initial-user", "initial-password-hash")

	km := NewKeyManager(db)
	key, err := km.InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		t.Fatalf("initialize encryption key: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key))
	}

	state, rawJSON := readPersistedAuthState(ctx, t, db)
	if state.Fingerprint == "" {
		t.Fatalf("expected fingerprint to be stored")
	}
	if strings.Contains(rawJSON, "helper") {
		t.Fatalf("expected persisted auth state to omit helper, got %s", rawJSON)
	}
}

func TestInitializeEncryptionKeyUnchangedFingerprintNormalizesLegacyState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := newTestCookieDB(t)
	dbPath := writeWebAuthFile(t, "same-user", "same-password-hash")
	helpers, err := loadAuthHelpers(dbPath)
	if err != nil {
		t.Fatalf("load auth helpers: %v", err)
	}
	if len(helpers) == 0 {
		t.Fatal("expected at least one auth helper candidate")
	}
	helper := helpers[0].Helper
	fingerprint := helpers[0].Fingerprint
	authStateJSON, err := json.Marshal(map[string]string{
		"helper":      helper,
		"fingerprint": fingerprint,
	})
	if err != nil {
		t.Fatalf("marshal legacy auth state: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		cookieAuthStateConfigSection, string(authStateJSON)); err != nil {
		t.Fatalf("insert legacy auth state: %v", err)
	}

	km := NewKeyManager(db)
	gotKey, err := km.InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		t.Fatalf("initialize encryption key: %v", err)
	}

	expectedSalt, err := getSaltFromDB(ctx, db)
	if err != nil {
		t.Fatalf("get salt: %v", err)
	}
	expectedKey, err := DeriveEncryptionKey(helper, expectedSalt)
	if err != nil {
		t.Fatalf("derive expected key: %v", err)
	}
	if !bytes.Equal(gotKey, expectedKey) {
		t.Fatalf("expected derived key to remain stable")
	}

	_, rawJSON := readPersistedAuthState(ctx, t, db)
	if strings.Contains(rawJSON, "helper") {
		t.Fatalf("expected legacy helper to be removed, got %s", rawJSON)
	}
}

func TestInitializeEncryptionKeyChangedFingerprintWithoutRecoverableHelper(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := newTestCookieDB(t)
	dbPath := writeWebAuthFile(t, "new-user", "new-password-hash")

	oldHelper := "old-user:old-password-hash"
	oldFingerprint := helperFingerprint(t, oldHelper)
	if err := storeAuthStateInDB(ctx, db, authState{Fingerprint: oldFingerprint}); err != nil {
		t.Fatalf("store legacy fingerprint: %v", err)
	}

	salt, err := ensureSalt(ctx, db)
	if err != nil {
		t.Fatalf("ensure salt: %v", err)
	}
	oldKey, err := DeriveEncryptionKey(oldHelper, salt)
	if err != nil {
		t.Fatalf("derive old key: %v", err)
	}

	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}
	if err := store.SaveCookie(ctx, "tracker", "session", "cookie-value", oldKey); err != nil {
		t.Fatalf("save cookie with old key: %v", err)
	}

	before := readCookieCiphertext(ctx, t, db, "tracker", "session")

	km := NewKeyManager(db)
	_, err = km.InitializeEncryptionKey(ctx, dbPath)
	if !errors.Is(err, ErrAuthHelperUnavailable) {
		t.Fatalf("expected ErrAuthHelperUnavailable, got %v", err)
	}

	after := readCookieCiphertext(ctx, t, db, "tracker", "session")
	if before != after {
		t.Fatalf("expected encrypted cookie row to remain unchanged")
	}

	state, _ := readPersistedAuthState(ctx, t, db)
	if state.Fingerprint != oldFingerprint {
		t.Fatalf("expected stored fingerprint to remain unchanged, got %q", state.Fingerprint)
	}

	if _, err := store.GetCookie(ctx, "tracker", "session", oldKey); err != nil {
		t.Fatalf("expected old key to continue decrypting cookie: %v", err)
	}
}

func TestInitializeEncryptionKeyErrorCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		setupWebAuth    func(t *testing.T) string
		wantErr         error
		wantErrContains string
	}{
		{
			name: "missing web auth returns unavailable",
			setupWebAuth: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing.sqlite")
			},
			wantErr: ErrAuthHelperUnavailable,
		},
		{
			name: "malformed web auth returns wrapped error",
			setupWebAuth: func(t *testing.T) string {
				dir := t.TempDir()
				dbPath := filepath.Join(dir, "malformed.sqlite")
				if err := os.WriteFile(filepath.Join(dir, authmaterial.WebAuthFileName), []byte("{"), 0o600); err != nil {
					t.Fatalf("write malformed web auth: %v", err)
				}
				return dbPath
			},
			wantErrContains: "failed to parse web auth file",
		},
		{
			name: "incomplete web auth returns unavailable",
			setupWebAuth: func(t *testing.T) string {
				dir := t.TempDir()
				dbPath := filepath.Join(dir, "incomplete.sqlite")
				if err := os.WriteFile(filepath.Join(dir, authmaterial.WebAuthFileName), []byte(`{"username":"tester"}`), 0o600); err != nil {
					t.Fatalf("write incomplete web auth: %v", err)
				}
				return dbPath
			},
			wantErr: ErrAuthHelperUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			db := newTestCookieDB(t)
			km := NewKeyManager(db)
			dbPath := test.setupWebAuth(t)

			_, err := km.InitializeEncryptionKey(ctx, dbPath)
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("expected %v, got %v", test.wantErr, err)
			}
			if test.wantErrContains != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrContains) {
					t.Fatalf("expected error containing %q, got %v", test.wantErrContains, err)
				}
			}
		})
	}
}

func TestRewrapCookiesWithAuthChangeMigratesLegacyHelperToStableSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := newTestCookieDB(t)

	oldMaterial := authmaterial.Material{
		Username:     "tester",
		PasswordHash: "legacy-password-hash",
	}
	newMaterial := authmaterial.Material{
		Username:          "tester",
		PasswordHash:      "upgraded-password-hash",
		EncryptionKeySeed: "stable-seed-value",
	}

	salt, err := ensureSalt(ctx, db)
	if err != nil {
		t.Fatalf("ensure salt: %v", err)
	}
	oldHelper, oldFingerprint, err := oldMaterial.PrimaryHelper()
	if err != nil {
		t.Fatalf("old helper: %v", err)
	}
	oldKey, err := DeriveEncryptionKey(oldHelper, salt)
	if err != nil {
		t.Fatalf("derive old key: %v", err)
	}

	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}
	if err := store.SaveCookie(ctx, "tracker", "session", "cookie-value", oldKey); err != nil {
		t.Fatalf("save legacy cookie: %v", err)
	}
	if err := storeAuthStateInDB(ctx, db, authState{Fingerprint: oldFingerprint}); err != nil {
		t.Fatalf("store legacy auth state: %v", err)
	}

	if err := RewrapCookiesWithAuthChange(ctx, db, oldMaterial, newMaterial); err != nil {
		t.Fatalf("rewrap cookies: %v", err)
	}

	newHelper, newFingerprint, err := newMaterial.PrimaryHelper()
	if err != nil {
		t.Fatalf("new helper: %v", err)
	}
	newKey, err := DeriveEncryptionKey(newHelper, salt)
	if err != nil {
		t.Fatalf("derive new key: %v", err)
	}

	value, err := store.GetCookie(ctx, "tracker", "session", newKey)
	if err != nil {
		t.Fatalf("decrypt cookie with new key: %v", err)
	}
	if value != "cookie-value" {
		t.Fatalf("expected cookie value to survive rewrap, got %q", value)
	}

	state, _ := readPersistedAuthState(ctx, t, db)
	if state.Fingerprint != newFingerprint {
		t.Fatalf("expected auth state fingerprint %q, got %q", newFingerprint, state.Fingerprint)
	}
}

func TestLoadAuthHelpersIgnoresDBFallbackSecrets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := newTestCookieDB(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		"MainSettings", `{"TMDBAPI":"old-tmdb-key-123"}`); err != nil {
		t.Fatalf("insert TMDB settings: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO config_settings (section, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		"Trackers", `{"Trackers":{"BHD":{"APIKey":"tracker-secret-token"}}}`); err != nil {
		t.Fatalf("insert tracker settings: %v", err)
	}

	helpers, err := loadAuthHelpers(filepath.Join(t.TempDir(), "missing.sqlite"))
	if !errors.Is(err, ErrAuthHelperUnavailable) {
		t.Fatalf("expected ErrAuthHelperUnavailable, got helpers=%v err=%v", helpers, err)
	}
}

func TestLoadAuthHelpersReturnsPrimaryAndLegacyCandidates(t *testing.T) {
	t.Parallel()

	dbPath := writeWebAuthFileWithSeed(t, "tester", "current-password-hash", "stable-seed-value")

	helpers, err := loadAuthHelpers(dbPath)
	if err != nil {
		t.Fatalf("load auth helpers: %v", err)
	}
	if len(helpers) != 2 {
		t.Fatalf("expected 2 helper candidates, got %d", len(helpers))
	}

	material := authmaterial.Material{
		Username:          "tester",
		PasswordHash:      "current-password-hash",
		EncryptionKeySeed: "stable-seed-value",
	}
	primaryHelper, primaryFingerprint, err := material.PrimaryHelper()
	if err != nil {
		t.Fatalf("primary helper: %v", err)
	}
	legacyHelper, legacyFingerprint, err := material.LegacyHelper()
	if err != nil {
		t.Fatalf("legacy helper: %v", err)
	}

	if helpers[0].Helper != primaryHelper || helpers[0].Fingerprint != primaryFingerprint {
		t.Fatalf("expected primary helper first, got %#v", helpers[0])
	}
	if helpers[1].Helper != legacyHelper || helpers[1].Fingerprint != legacyFingerprint {
		t.Fatalf("expected legacy helper second, got %#v", helpers[1])
	}

	if recovered, ok := findAuthHelperByFingerprint(helpers, legacyFingerprint); !ok || recovered != legacyHelper {
		t.Fatalf("expected to recover legacy helper by fingerprint, got helper=%q ok=%v", recovered, ok)
	}
}

func newTestCookieDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	statements := []string{
		`CREATE TABLE config_settings (
			section TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			updated_at DATETIME
		)`,
		`CREATE TABLE tracker_cookies (
			tracker_id TEXT NOT NULL,
			cookie_name TEXT NOT NULL,
			encrypted_value TEXT NOT NULL,
			nonce TEXT NOT NULL,
			auth_tag TEXT NOT NULL,
			created_at DATETIME,
			updated_at DATETIME,
			PRIMARY KEY (tracker_id, cookie_name)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}

	return db
}

func writeWebAuthFile(t *testing.T, username, passwordHash string) string {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")
	payload, err := json.Marshal(map[string]string{
		"username":      username,
		"password_hash": passwordHash,
	})
	if err != nil {
		t.Fatalf("marshal web auth file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, authmaterial.WebAuthFileName), payload, 0o600); err != nil {
		t.Fatalf("write web auth file: %v", err)
	}

	return dbPath
}

func writeWebAuthFileWithSeed(t *testing.T, username, passwordHash, seed string) string {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")
	payload, err := json.Marshal(map[string]string{
		"username":            username,
		"password_hash":       passwordHash,
		"encryption_key_seed": seed,
	})
	if err != nil {
		t.Fatalf("marshal web auth file with seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, authmaterial.WebAuthFileName), payload, 0o600); err != nil {
		t.Fatalf("write web auth file with seed: %v", err)
	}

	return dbPath
}

func readPersistedAuthState(ctx context.Context, t *testing.T, db *sql.DB) (authState, string) {
	t.Helper()

	var rawJSON string
	if err := db.QueryRowContext(ctx, `SELECT data FROM config_settings WHERE section = ?`, cookieAuthStateConfigSection).Scan(&rawJSON); err != nil {
		t.Fatalf("query auth state JSON: %v", err)
	}

	state, err := getAuthStateFromDB(ctx, db)
	if err != nil {
		t.Fatalf("get auth state from db: %v", err)
	}

	return state, rawJSON
}

func readCookieCiphertext(ctx context.Context, t *testing.T, db *sql.DB, trackerID, cookieName string) string {
	t.Helper()

	var ciphertext, nonce, authTag string
	if err := db.QueryRowContext(ctx,
		`SELECT encrypted_value, nonce, auth_tag FROM tracker_cookies WHERE tracker_id = ? AND cookie_name = ?`,
		trackerID, cookieName,
	).Scan(&ciphertext, &nonce, &authTag); err != nil {
		t.Fatalf("query encrypted cookie row: %v", err)
	}

	return strings.Join([]string{ciphertext, nonce, authTag}, "|")
}

func helperFingerprint(t *testing.T, helper string) string {
	t.Helper()

	rawHelper := strings.TrimSpace(helper)
	if rawHelper == "" {
		t.Fatalf("helper must not be empty")
	}

	sum := sha256.Sum256([]byte(rawHelper))
	return hex.EncodeToString(sum[:])
}
