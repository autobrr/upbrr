// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"slices"
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

	rows, err := rawDB.Query(`SELECT id FROM schema_migrations ORDER BY id`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema_migrations: %v", err)
	}
	if !slices.Contains(ids, "2026_04_add_tracker_cookies") {
		t.Fatalf("expected tracker cookies migration id to be recorded, got %v", ids)
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

func TestMigrateBridgesLegacyV8TrackerCookiesSchema(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	ctx := context.Background()
	if err := createBaselineSchema(ctx, rawDB); err != nil {
		t.Fatalf("create baseline schema: %v", err)
	}
	if err := migrateAddDVDMediaInfo(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v2: %v", err)
	}
	if err := migrateAddReleaseOverrideUseSeasonEpisode(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v3: %v", err)
	}
	if err := migrateAddHistoryIndexes(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v4: %v", err)
	}
	if err := migrateBackfillUploadedImageUsageScope(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v5: %v", err)
	}
	if err := migrateAddScreenshotSlotTables(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v6: %v", err)
	}
	if err := migrateNormalizeDescriptionOverrides(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v7: %v", err)
	}
	if err := migrateAddTrackerCookies(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v8: %v", err)
	}
	if _, err := rawDB.Exec(`PRAGMA user_version = 8`); err != nil {
		t.Fatalf("set legacy user_version: %v", err)
	}

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("bridge legacy v8 db: %v", err)
	}

	var userVersion int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("query user_version after bridge: %v", err)
	}
	if userVersion != expectedSchemaVersion {
		t.Fatalf("expected schema version %d after bridge, got %d", expectedSchemaVersion, userVersion)
	}

	assertSQLiteObjectExists(t, rawDB, "table", "tracker_cookies")
	assertSQLiteObjectExists(t, rawDB, "table", "schema_migrations")
	assertSQLiteObjectExists(t, rawDB, "index", "idx_tracker_cookies_tracker_id")
	assertSQLiteObjectExists(t, rawDB, "index", "idx_tracker_cookies_created_at")
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
