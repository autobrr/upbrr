// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	servicedb "github.com/autobrr/upbrr/internal/services/db"
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
	result, err := MigrateFromFilesToDB(ctx, cookiesDir, store, key, api.NopLogger{})
	migratedCount, failedCookies := result.Count, result.Failures
	if err != nil {
		t.Fatalf("migrate cookies: %v", err)
	}
	if migratedCount != 0 {
		t.Fatalf("expected 0 migrated cookies, got %d", migratedCount)
	}
	if len(failedCookies) != 1 {
		t.Fatalf("expected 1 failed cookie, got %d", len(failedCookies))
	}
	if failedCookies[0].TrackerID != "TRACKER" || failedCookies[0].CookieName != "session" {
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracker_cookies WHERE tracker_id = ?`, "VALID").Scan(&storedCount); err != nil {
		t.Fatalf("count migrated cookies: %v", err)
	}
	if storedCount != 1 {
		t.Fatalf("expected valid cookie to migrate, got count=%d", storedCount)
	}
}

func TestLegacyCookieMigrationWaitsForCookieDeletion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	repo, err := servicedb.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	deleting, err := NewCookieStore(repo.RawDB())
	if err != nil {
		t.Fatal(err)
	}
	migrationDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrationDB.Close() })
	migrationDB.SetMaxOpenConns(1)
	if _, err := migrationDB.ExecContext(ctx, `PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	migrating, err := NewCookieStore(migrationDB)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "TL.json")
	if err := os.WriteFile(path, []byte(`{"session":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if err := deleting.RunInTransaction(ctx, func(tx *sql.Tx) error {
		if err := deleting.DeleteAllTrackerCookiesTx(ctx, tx, "TL"); err != nil {
			return err
		}
		result, err := MigrateFromFilesToDB(ctx, dir, migrating, key, api.NopLogger{})
		if !servicedb.IsBusyError(err) || result.Count != 0 || len(result.Failures) != 0 {
			return errors.New("migration began processing the export before deletion finished")
		}
		return os.Remove(path)
	}); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateFromFilesToDB(ctx, dir, migrating, key, api.NopLogger{})
	if err != nil || result.Count != 0 || len(result.Failures) != 0 {
		t.Fatalf("migration after deletion: count=%d failures=%d error=%v", result.Count, len(result.Failures), err)
	}
}

func TestLegacyCookieMigrationPreservesManagedSessions(t *testing.T) {
	t.Parallel()
	for _, transactional := range []bool{false, true} {
		name := "startup"
		if transactional {
			name = "config activation"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			db := newTestCookieDB(t)
			store, err := NewCookieStore(db)
			if err != nil {
				t.Fatal(err)
			}
			key := make([]byte, 32)
			if err := store.SaveCookie(ctx, "TL", trackerCookieOwnerKey, "fixture-owner", key); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "tl.json")
			if err := os.WriteFile(path, []byte(`{"session":"expired"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			sibling := filepath.Join(dir, "`N.json")
			if err := os.WriteFile(sibling, []byte(`{"session":"fixture-active"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var result MigrationResult
			if transactional {
				err = store.RunInTransaction(ctx, func(tx *sql.Tx) error {
					var migrationErr error
					result, migrationErr = MigrateFromFilesToDBTx(ctx, dir, store, tx, key, api.NopLogger{})
					return migrationErr
				})
			} else {
				result, err = MigrateFromFilesToDB(ctx, dir, store, key, api.NopLogger{})
			}
			if err != nil || result.Count != 1 || len(result.Failures) != 0 {
				t.Fatalf("legacy migration count=%d failures=%d error=%v", result.Count, len(result.Failures), err)
			}
			if err := result.Cleanup(dir, api.NopLogger{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(sibling); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("the successfully migrated sibling file remained eligible for import")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("the retained managed session file was removed")
			}
			stored, err := store.GetAllTrackerCookies(ctx, "TL", key)
			if err != nil || len(stored) != 1 || stored[trackerCookieOwnerKey] != "fixture-owner" {
				t.Fatal("migration changed the invalidated managed session")
			}
			if _, err := store.GetCookie(ctx, "tl", "session", key); !errors.Is(err, ErrCookieNotFound) {
				t.Fatal("migration reinstalled the retained legacy session")
			}
		})
	}
}

func TestCookieMigrationRetainsNewerExport(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, err := NewCookieStore(newTestCookieDB(t))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	dir := t.TempDir()
	path := filepath.Join(dir, "tl.JSON")
	if err := os.WriteFile(path, []byte(`{"session":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateFromFilesToDB(ctx, dir, store, key, api.NopLogger{})
	if err != nil || result.Count != 1 || len(result.Failures) != 0 {
		t.Fatalf("migration count=%d failures=%d error=%v", result.Count, len(result.Failures), err)
	}
	if err := os.Rename(path, filepath.Join(dir, "old-export")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"session":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := result.Cleanup(dir, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("new export was removed: %v", err)
	}
	value, err := store.GetCookie(ctx, "TL", "session", key)
	if err != nil || value != "old" {
		t.Fatal("migration did not store the old export using the canonical tracker ID")
	}
}

func TestCookieMigrationPrefersTheSameExportAsLegacyLoading(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)
	path, err := servicedb.CookiePath(dbPath, "TL.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.WriteFile(path, []byte(".torrentleech.org\tTRUE\t/\tTRUE\t0\tsession\tpreferred\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "tl.JSON")
	if err := os.WriteFile(alias, []byte(`{"session":"fallback"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := servicedb.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	for _, phase := range []string{"before", "after"} {
		if phase == "after" {
			if err := EnsureCookieMigration(ctx, repo.RawDB(), dbPath, dir, api.NopLogger{}); err != nil {
				t.Fatal(err)
			}
		}
		values, err := LoadTrackerCookieMap(ctx, dbPath, "TL")
		if err != nil || values["session"] != "preferred" {
			t.Fatalf("%s migration: a lower-priority export won: %v", phase, err)
		}
	}
	for _, exported := range []string{path, alias} {
		if _, err := os.Stat(exported); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("migration left an imported export available for replay: %v", err)
		}
	}
}
