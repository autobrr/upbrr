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
	if err := rawDB.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	if userVersion != expectedSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", expectedSchemaVersion, userVersion)
	}

	rows, err := rawDB.QueryContext(context.Background(), `SELECT id FROM schema_migrations ORDER BY id`)
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
	if !slices.Contains(ids, "2026_06_add_tracker_auth_state") {
		t.Fatalf("expected tracker auth state migration id to be recorded, got %v", ids)
	}

	objects := []struct {
		typ  string
		name string
	}{
		{typ: "table", name: "tracker_cookies"},
		{typ: "index", name: "idx_tracker_cookies_tracker_id"},
		{typ: "index", name: "idx_tracker_cookies_created_at"},
		{typ: "table", name: "tracker_auth_state"},
		{typ: "index", name: "idx_tracker_auth_state_tracker_id"},
		{typ: "index", name: "idx_tracker_auth_state_updated_at"},
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
	if _, err := rawDB.ExecContext(context.Background(), `PRAGMA user_version = 8`); err != nil {
		t.Fatalf("set legacy user_version: %v", err)
	}

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("bridge legacy v8 db: %v", err)
	}

	var userVersion int
	if err := rawDB.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&userVersion); err != nil {
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

func TestMigrateAddExternalIDsMALBackfillsFromTMDBMetadata(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	ctx := context.Background()
	statements := []string{
		`
		CREATE TABLE external_ids (
			source_path TEXT PRIMARY KEY,
			tmdb_id INTEGER NOT NULL DEFAULT 0,
			imdb_id INTEGER NOT NULL DEFAULT 0,
			tvdb_id INTEGER NOT NULL DEFAULT 0,
			tvmaze_id INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT "",
			source_tmdb TEXT NOT NULL DEFAULT "",
			source_imdb TEXT NOT NULL DEFAULT "",
			source_tvdb TEXT NOT NULL DEFAULT "",
			source_tvmaze TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
		`,
		`
		CREATE TABLE external_metadata (
			source_path TEXT PRIMARY KEY,
			tmdb_json TEXT NOT NULL DEFAULT "",
			imdb_json TEXT NOT NULL DEFAULT "",
			tvdb_json TEXT NOT NULL DEFAULT "",
			tvmaze_json TEXT NOT NULL DEFAULT "",
			bluray_json TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
		`,
		`INSERT INTO external_ids (source_path, tmdb_id, updated_at) VALUES ('/media/Example.Release.2026.1080p-GRP.mkv', 123, '2026-01-01T00:00:00Z')`,
		`INSERT INTO external_ids (source_path, tmdb_id, updated_at) VALUES ('/media/Example.Release.2026.2160p-GRP.mkv', 456, '2026-01-01T00:00:00Z')`,
		`INSERT INTO external_metadata (source_path, tmdb_json, updated_at) VALUES ('/media/Example.Release.2026.1080p-GRP.mkv', '{"MALID":5114}', '2026-01-01T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := rawDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed schema: %v", err)
		}
	}

	if err := migrateAddExternalIDsMAL(ctx, rawDB); err != nil {
		t.Fatalf("migrate external ids mal: %v", err)
	}

	var malID int
	var sourceMAL string
	if err := rawDB.QueryRowContext(ctx, `
		SELECT mal_id, source_mal
		FROM external_ids
		WHERE source_path = ?
	`, "/media/Example.Release.2026.1080p-GRP.mkv").Scan(&malID, &sourceMAL); err != nil {
		t.Fatalf("read backfilled mal: %v", err)
	}
	if malID != 5114 || sourceMAL != "tmdb" {
		t.Fatalf("expected tmdb mal backfill, got id=%d source=%q", malID, sourceMAL)
	}

	if err := rawDB.QueryRowContext(ctx, `
		SELECT mal_id, source_mal
		FROM external_ids
		WHERE source_path = ?
	`, "/media/Example.Release.2026.2160p-GRP.mkv").Scan(&malID, &sourceMAL); err != nil {
		t.Fatalf("read empty mal row: %v", err)
	}
	if malID != 0 || sourceMAL != "" {
		t.Fatalf("expected missing metadata to stay empty, got id=%d source=%q", malID, sourceMAL)
	}
}

func TestMigrateAddAniListExternalMetadataCreatesMissingTable(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	ctx := context.Background()
	if err := migrateAddAniListExternalMetadata(ctx, rawDB); err != nil {
		t.Fatalf("migrate anilist external metadata: %v", err)
	}
	if err := migrateAddAniListExternalMetadata(ctx, rawDB); err != nil {
		t.Fatalf("migrate anilist external metadata idempotent: %v", err)
	}

	assertSQLiteObjectExists(t, rawDB, "table", "external_metadata")
	assertSQLiteObjectExists(t, rawDB, "index", "idx_external_metadata_source_path")

	for _, column := range []string{
		"source_path",
		"tmdb_json",
		"imdb_json",
		"tvdb_json",
		"tvmaze_json",
		"anilist_json",
		"bluray_json",
		"updated_at",
	} {
		exists, err := tableColumnExists(ctx, rawDB, "external_metadata", column)
		if err != nil {
			t.Fatalf("inspect external_metadata.%s: %v", column, err)
		}
		if !exists {
			t.Fatalf("expected external_metadata.%s to exist", column)
		}
	}
}

func assertSQLiteObjectExists(t *testing.T, db *sql.DB, objectType, name string) {
	t.Helper()

	var count int
	if err := db.QueryRowContext(context.Background(),
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
