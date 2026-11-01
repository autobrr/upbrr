// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"errors"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotUsesExistingKeyWithoutWrites(t *testing.T) {
	t.Parallel()
	db := newTestCookieDB(t)
	db.SetMaxOpenConns(1)
	path := writeWebAuthFile(t, "synthetic-user", "synthetic-hash")
	key, err := NewKeyManager(db).InitializeEncryptionKey(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for tracker, value := range map[string]string{"AZ": "selected-session", "CZ": "other-session"} {
		if err := store.SaveCookie(t.Context(), tracker, "session", value, key); err != nil {
			t.Fatal(err)
		}
	}
	// Even initial auth-state normalization is forbidden during a draft probe.
	if _, err := db.ExecContext(t.Context(), `DELETE FROM config_settings WHERE section = ?`, cookieAuthStateConfigSection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotTrackerCookieMap(t.Context(), db, path, "AZ", "avistaz.to")
	if err != nil || len(got) != 1 || got["session"] != "selected-session" {
		t.Fatalf("snapshot=%v err=%v", got, err)
	}
	got["session"] = "changed-only-in-memory"
	stored, err := store.GetAllTrackerCookies(t.Context(), "AZ", key)
	if err != nil || stored["session"] != "selected-session" {
		t.Fatal("snapshot changed persisted cookies")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM config_settings WHERE section = ?`, cookieAuthStateConfigSection).Scan(&count); err != nil || count != 0 {
		t.Fatal("snapshot initialized auth state")
	}
}

func TestSnapshotDoesNotInitializeEmptyStorage(t *testing.T) {
	t.Parallel()
	db := newTestCookieDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	_, err := SnapshotTrackerCookieMap(t.Context(), db, filepath.Join(t.TempDir(), "absent", "must-not-create.db"), "AZ", "avistaz.to")
	if !errors.Is(err, ErrTrackerCookiesNotFound) {
		t.Fatalf("empty snapshot: %v", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM config_settings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("snapshot initialized encryption metadata")
	}
}

func TestSnapshotFiltersLegacyCookieDomains(t *testing.T) {
	t.Parallel()
	db := newTestCookieDB(t)
	path := filepath.Join(t.TempDir(), "server.db")
	candidate := commonhttp.CookiePathCandidates(path, "AZ", ".txt")[0]
	if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := "# Netscape HTTP Cookie File\n.avistaz.to\tTRUE\t/\tTRUE\t2147483647\tsession\tselected\n.other.example\tTRUE\t/\tTRUE\t2147483647\tunrelated\tmust-not-send\n"
	if err := os.WriteFile(candidate, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotTrackerCookieMap(t.Context(), db, path, "AZ", "avistaz.to")
	if err != nil || len(got) != 1 || got["session"] != "selected" {
		t.Fatalf("scoped snapshot=%v err=%v", got, err)
	}
}
