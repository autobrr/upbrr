// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMigrateFromFilesToDBReturnsFailedCookies(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}

	cookiesDir := t.TempDir()
	payload, err := json.Marshal(map[string]string{
		"session": "cookie-value",
		"":        "bad-cookie",
	})
	if err != nil {
		t.Fatalf("marshal cookie payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cookiesDir, "tracker.json"), payload, 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	ctx := context.Background()
	key := []byte("short")
	migratedCount, failedCookies, err := MigrateFromFilesToDB(ctx, cookiesDir, store, key, api.NopLogger{})
	if err != nil {
		t.Fatalf("migrate cookies: %v", err)
	}
	if migratedCount != 0 {
		t.Fatalf("expected 0 migrated cookies, got %d", migratedCount)
	}
	if len(failedCookies) != 1 {
		t.Fatalf("expected 1 failed cookie, got %d", len(failedCookies))
	}
	if failedCookies[0].TrackerID != "tracker" || failedCookies[0].CookieName != "session" {
		t.Fatalf("unexpected failed cookie entry: %+v", failedCookies[0])
	}
}

func TestEnsureCookieMigrationKeepsLegacyFilesOnPartialFailure(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE config_settings (
		section TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		updated_at DATETIME
	)`); err != nil {
		t.Fatalf("create config_settings table: %v", err)
	}

	dbPath := writeWebAuthFile(t, "admin", "password-hash")
	cookiesDir := t.TempDir()

	payload, err := json.Marshal(map[string]string{
		"session": "cookie-value",
	})
	if err != nil {
		t.Fatalf("marshal cookie payload: %v", err)
	}
	legacyPath := filepath.Join(cookiesDir, "tracker.json")
	if err := os.WriteFile(legacyPath, payload, 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}

	ctx := context.Background()
	if err := EnsureCookieMigration(ctx, db, dbPath, cookiesDir, api.NopLogger{}); err != nil {
		t.Fatalf("ensure cookie migration: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("expected legacy cookie file to be preserved after partial failure: %v", err)
	}
}

func TestEnsureCookieMigrationKeepsAllLegacyFilesOnMixedParseResult(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	dbPath := writeWebAuthFile(t, "admin", "password-hash")
	cookiesDir := t.TempDir()
	validPath := filepath.Join(cookiesDir, "valid.json")
	invalidPath := filepath.Join(cookiesDir, "invalid.json")
	if err := os.WriteFile(validPath, []byte(`{"session":"cookie-value"}`), 0o600); err != nil {
		t.Fatalf("write valid cookie file: %v", err)
	}
	if err := os.WriteFile(invalidPath, []byte(`{`), 0o600); err != nil {
		t.Fatalf("write invalid cookie file: %v", err)
	}

	ctx := context.Background()
	if err := EnsureCookieMigration(ctx, db, dbPath, cookiesDir, api.NopLogger{}); err != nil {
		t.Fatalf("ensure cookie migration: %v", err)
	}
	for _, legacyPath := range []string{validPath, invalidPath} {
		if _, err := os.Stat(legacyPath); err != nil {
			t.Fatalf("expected every legacy file to remain after mixed parse result: %v", err)
		}
	}

	var storedCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracker_cookies WHERE tracker_id = ?`, "valid").Scan(&storedCount); err != nil {
		t.Fatalf("count migrated cookies: %v", err)
	}
	if storedCount != 1 {
		t.Fatalf("expected valid cookie to migrate, got count=%d", storedCount)
	}
}
