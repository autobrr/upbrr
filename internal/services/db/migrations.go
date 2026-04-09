// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const baselineSchemaVersion = 1

type migrationStep struct {
	version int
	apply   func(context.Context, migrationExecutor) error
}

type migrationExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Add new forward-only migrations here.
// Example:
// {version: 2, apply: migrateV2},
var futureMigrations = []migrationStep{
	{version: 2, apply: migrateV2},
	{version: 3, apply: migrateV3},
	{version: 4, apply: migrateV4},
	{version: 5, apply: migrateV5},
	{version: 6, apply: migrateV6},
}

func migrateV2(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`
		CREATE TABLE IF NOT EXISTS dvd_mediainfo (
			source_path TEXT PRIMARY KEY,
			ifo_path TEXT NOT NULL DEFAULT "",
			vob_path TEXT NOT NULL DEFAULT "",
			vob_set TEXT NOT NULL DEFAULT "",
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			frame_rate TEXT NOT NULL DEFAULT "",
			scan_type TEXT NOT NULL DEFAULT "",
			resolution TEXT NOT NULL DEFAULT "",
			high_frame_rate INTEGER NOT NULL DEFAULT 0,
			mediainfo_json TEXT NOT NULL DEFAULT "",
			mediainfo_text TEXT NOT NULL DEFAULT "",
			vob_mediainfo_raw TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_dvd_mediainfo_vob_set ON dvd_mediainfo(vob_set)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}

func migrateV3(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`ALTER TABLE release_overrides ADD COLUMN use_season_episode INTEGER`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}

func migrateV4(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_file_metadata_updated_at ON file_metadata(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_records_source_created ON upload_records(source_path, created_at DESC)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}

func migrateV5(ctx context.Context, exec migrationExecutor) error {
	statements := make([]string, 0, 5)
	exists, err := tableColumnExists(ctx, exec, "uploaded_images", "usage_scope")
	if err != nil {
		return err
	}
	if !exists {
		statements = append(statements, `ALTER TABLE uploaded_images ADD COLUMN usage_scope TEXT NOT NULL DEFAULT "global"`)
	}
	statements = append(statements,
		`UPDATE uploaded_images SET usage_scope = "global" WHERE TRIM(usage_scope) = ""`,
		`DROP INDEX IF EXISTS idx_uploaded_images_unique`,
		`CREATE INDEX IF NOT EXISTS idx_uploaded_images_source_scope ON uploaded_images (source_path, usage_scope)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_uploaded_images_unique ON uploaded_images (source_path, usage_scope, host, image_path)`,
	)

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}

func migrateV6(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`
		CREATE TABLE IF NOT EXISTS screenshot_slots (
			source_path TEXT NOT NULL,
			slot_order INTEGER NOT NULL,
			source_kind TEXT NOT NULL DEFAULT "",
			original_key TEXT NOT NULL DEFAULT "",
			original_url TEXT NOT NULL DEFAULT "",
			original_host TEXT NOT NULL DEFAULT "",
			image_path TEXT NOT NULL DEFAULT "",
			from_description INTEGER NOT NULL DEFAULT 0,
			tracker TEXT NOT NULL DEFAULT "",
			section_kind TEXT NOT NULL DEFAULT "",
			render_in_screenshots INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (source_path, slot_order)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_screenshot_slots_source_order ON screenshot_slots (source_path, slot_order)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshot_slots_source_key ON screenshot_slots (source_path, original_key)`,
		`
		CREATE TABLE IF NOT EXISTS screenshot_slot_variants (
			source_path TEXT NOT NULL,
			slot_order INTEGER NOT NULL,
			host TEXT NOT NULL,
			usage_scope TEXT NOT NULL DEFAULT "global",
			image_path TEXT NOT NULL DEFAULT "",
			img_url TEXT NOT NULL DEFAULT "",
			raw_url TEXT NOT NULL DEFAULT "",
			web_url TEXT NOT NULL DEFAULT "",
			uploaded_at TEXT NOT NULL DEFAULT "",
			PRIMARY KEY (source_path, slot_order, usage_scope, host)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_screenshot_slot_variants_source_slot ON screenshot_slot_variants (source_path, slot_order)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshot_slot_variants_source_scope_host ON screenshot_slot_variants (source_path, slot_order, usage_scope, host)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}

func tableColumnExists(ctx context.Context, exec migrationExecutor, tableName string, columnName string) (bool, error) {
	rows, err := exec.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if strings.EqualFold(name, columnName) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func Migrate(db *sql.DB) error {
	return MigrateContext(context.Background(), db)
}

func MigrateContext(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("db: nil connection")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("db migrate conn: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("db migrate begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	latestVersion, err := latestSchemaVersion()
	if err != nil {
		return err
	}

	currentVersion, err := readUserVersion(ctx, conn)
	if err != nil {
		return err
	}

	switch {
	case currentVersion == 0:
		if err := createBaselineSchema(ctx, conn); err != nil {
			return fmt.Errorf("db bootstrap baseline: %w", err)
		}
		if err := writeUserVersion(ctx, conn, baselineSchemaVersion); err != nil {
			return err
		}
		currentVersion = baselineSchemaVersion
	case currentVersion > latestVersion:
		return fmt.Errorf("db migrate: database schema version %d is newer than application version %d", currentVersion, latestVersion)
	}

	if err := applyFutureMigrations(ctx, conn, currentVersion); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("db migrate commit: %w", err)
	}
	committed = true

	return nil
}

func latestSchemaVersion() (int, error) {
	expected := baselineSchemaVersion + 1
	for _, step := range futureMigrations {
		if step.apply == nil {
			return 0, fmt.Errorf("db migrate: nil migration function for version %d", step.version)
		}
		if step.version != expected {
			return 0, fmt.Errorf("db migrate: migration versions must be contiguous, expected v%d got v%d", expected, step.version)
		}
		expected++
	}
	return expected - 1, nil
}

func applyFutureMigrations(ctx context.Context, exec migrationExecutor, currentVersion int) error {
	for _, step := range futureMigrations {
		if step.version <= currentVersion {
			continue
		}
		if err := step.apply(ctx, exec); err != nil {
			return fmt.Errorf("db migrate v%d: %w", step.version, err)
		}
		if err := writeUserVersion(ctx, exec, step.version); err != nil {
			return err
		}
		currentVersion = step.version
	}
	return nil
}

func readUserVersion(ctx context.Context, exec migrationExecutor) (int, error) {
	var version int
	if err := exec.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("db migrate: read user_version: %w", err)
	}
	return version, nil
}

func writeUserVersion(ctx context.Context, exec migrationExecutor, version int) error {
	if _, err := exec.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return fmt.Errorf("db migrate: write user_version: %w", err)
	}
	return nil
}

func createBaselineSchema(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`
		CREATE TABLE IF NOT EXISTS file_metadata (
			path TEXT PRIMARY KEY,
			info_hash TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL,
			disc_type TEXT NOT NULL DEFAULT "",
			video_path TEXT NOT NULL DEFAULT "",
			file_list TEXT NOT NULL DEFAULT "[]",
			scene INTEGER NOT NULL DEFAULT 0,
			scene_name TEXT NOT NULL DEFAULT "",
			scene_imdb INTEGER NOT NULL DEFAULT 0,
			release_type TEXT NOT NULL DEFAULT "",
			release_artist TEXT NOT NULL DEFAULT "",
			release_title TEXT NOT NULL DEFAULT "",
			release_subtitle TEXT NOT NULL DEFAULT "",
			release_alt TEXT NOT NULL DEFAULT "",
			release_year INTEGER NOT NULL DEFAULT 0,
			release_month INTEGER NOT NULL DEFAULT 0,
			release_day INTEGER NOT NULL DEFAULT 0,
			release_source TEXT NOT NULL DEFAULT "",
			release_resolution TEXT NOT NULL DEFAULT "",
			release_codec TEXT NOT NULL DEFAULT "[]",
			release_audio TEXT NOT NULL DEFAULT "[]",
			release_hdr TEXT NOT NULL DEFAULT "[]",
			release_ext TEXT NOT NULL DEFAULT "",
			release_language TEXT NOT NULL DEFAULT "[]",
			release_site TEXT NOT NULL DEFAULT "",
			release_genre TEXT NOT NULL DEFAULT "",
			release_channels TEXT NOT NULL DEFAULT "",
			release_collection TEXT NOT NULL DEFAULT "",
			release_region TEXT NOT NULL DEFAULT "",
			release_size TEXT NOT NULL DEFAULT "",
			release_group TEXT NOT NULL DEFAULT "",
			release_disc TEXT NOT NULL DEFAULT "",
			release_edition TEXT NOT NULL DEFAULT "[]",
			release_other TEXT NOT NULL DEFAULT "[]",
			source_size INTEGER NOT NULL DEFAULT 0
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS upload_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tracker TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			source_path TEXT NOT NULL DEFAULT ""
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_upload_records_status ON upload_records (status)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_records_source_path ON upload_records (source_path)`,
		`
		CREATE TABLE IF NOT EXISTS tracker_metadata (
			source_path TEXT NOT NULL,
			tracker TEXT NOT NULL,
			tracker_id TEXT NOT NULL DEFAULT "",
			info_hash TEXT NOT NULL DEFAULT "",
			tmdb_id INTEGER NOT NULL DEFAULT 0,
			imdb_id INTEGER NOT NULL DEFAULT 0,
			tvdb_id INTEGER NOT NULL DEFAULT 0,
			mal_id INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT "",
			description TEXT NOT NULL DEFAULT "",
			image_urls TEXT NOT NULL DEFAULT "[]",
			filename TEXT NOT NULL DEFAULT "",
			matched INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (source_path, tracker)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_metadata_source_path ON tracker_metadata (source_path)`,
		`
		CREATE TABLE IF NOT EXISTS tracker_timestamps (
			tracker TEXT PRIMARY KEY,
			updated_at TEXT NOT NULL
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS external_ids (
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
		CREATE TABLE IF NOT EXISTS external_metadata (
			source_path TEXT PRIMARY KEY,
			tmdb_json TEXT NOT NULL DEFAULT "",
			imdb_json TEXT NOT NULL DEFAULT "",
			tvdb_json TEXT NOT NULL DEFAULT "",
			tvmaze_json TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_external_ids_source_path ON external_ids (source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_external_metadata_source_path ON external_metadata (source_path)`,
		`
		CREATE TABLE IF NOT EXISTS config_settings (
			section TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS release_overrides (
			source_path TEXT PRIMARY KEY,
			category TEXT,
			release_type TEXT,
			release_source TEXT,
			release_resolution TEXT,
			tag TEXT,
			service TEXT,
			edition TEXT,
			season TEXT,
			episode TEXT,
			episode_title TEXT,
			manual_year INTEGER,
			manual_date TEXT,
			no_season INTEGER,
			no_year INTEGER,
			no_aka INTEGER,
			no_tag INTEGER,
			no_edition INTEGER,
			no_dub INTEGER,
			no_dual INTEGER,
			dual_audio INTEGER,
			region TEXT,
			updated_at TEXT NOT NULL
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS screenshots (
			source_path TEXT NOT NULL,
			image_path TEXT NOT NULL PRIMARY KEY,
			timestamp REAL NOT NULL,
			frame_number INTEGER NOT NULL,
			width INTEGER NOT NULL,
			height INTEGER NOT NULL,
			purpose TEXT NOT NULL,
			captured_at TEXT NOT NULL
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_screenshots_source_path ON screenshots(source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshots_timestamp ON screenshots(source_path, timestamp)`,
		`
		CREATE TABLE IF NOT EXISTS screenshot_final_selections (
			source_path TEXT NOT NULL,
			image_path TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			source TEXT NOT NULL,
			selected_at TEXT NOT NULL,
			PRIMARY KEY (source_path, image_path)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_screenshot_final_path ON screenshot_final_selections(source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshot_final_order ON screenshot_final_selections(source_path, sort_order)`,
		`
		CREATE TABLE IF NOT EXISTS uploaded_images (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_path TEXT NOT NULL,
			image_path TEXT NOT NULL,
			host TEXT NOT NULL,
			usage_scope TEXT NOT NULL DEFAULT "global",
			img_url TEXT NOT NULL DEFAULT "",
			raw_url TEXT NOT NULL DEFAULT "",
			web_url TEXT NOT NULL DEFAULT "",
			size_bytes INTEGER NOT NULL DEFAULT 0,
			uploaded_at TEXT NOT NULL
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_uploaded_images_source_path ON uploaded_images (source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_uploaded_images_source_host ON uploaded_images (source_path, host)`,
		`CREATE INDEX IF NOT EXISTS idx_uploaded_images_source_scope ON uploaded_images (source_path, usage_scope)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_uploaded_images_unique ON uploaded_images (source_path, usage_scope, host, image_path)`,
		`
		CREATE TABLE IF NOT EXISTS description_overrides (
			source_path TEXT PRIMARY KEY,
			description TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS tracker_rule_failures (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_path TEXT NOT NULL,
			tracker TEXT NOT NULL,
			rule TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT "",
			created_at TEXT NOT NULL
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_rule_failures_source_path ON tracker_rule_failures (source_path)`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_rule_failures_tracker ON tracker_rule_failures (tracker)`,
		`
		CREATE TABLE IF NOT EXISTS playlist_selections (
			source_path TEXT PRIMARY KEY,
			selected_playlists TEXT NOT NULL DEFAULT "[]",
			use_all BOOLEAN NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_playlist_selections_updated ON playlist_selections (updated_at)`,
	}

	for idx, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db bootstrap statement %d: %w", idx+1, err)
		}
	}

	return nil
}
