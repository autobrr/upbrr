// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const expectedSchemaVersion = 8

func TestMigrateCreatesTrackerCookiesSchema(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var userVersion int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if userVersion != expectedSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", expectedSchemaVersion, userVersion)
	}

	objects := []struct {
		typ  string
		name string
	}{
		{typ: "table", name: "tracker_cookies"},
		{typ: "index", name: "idx_tracker_cookies_tracker_id"},
		{typ: "index", name: "idx_tracker_cookies_created_at"},
	}

	for _, item := range objects {
		assertSQLiteObjectExists(t, rawDB, item.typ, item.name)
	}
}

func assertSQLiteObjectExists(t *testing.T, db *sql.DB, objectType, name string) {
	t.Helper()

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(1) FROM sqlite_master WHERE type = ? AND name = ?`,
		objectType,
		name,
	).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master for %s %s: %v", objectType, name, err)
	}
	if count != 1 {
		t.Fatalf("expected %s %s to exist", objectType, name)
	}
}
