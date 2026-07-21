// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	baselineMigrationID              = "2026_04_baseline_schema"
	legacyCompatibilitySchemaVersion = 8
	schemaMigrationsTableDDL         = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`
	externalMetadataTableDDL = `
		CREATE TABLE IF NOT EXISTS external_metadata (
			source_path TEXT PRIMARY KEY,
			tmdb_json TEXT NOT NULL DEFAULT "",
			imdb_json TEXT NOT NULL DEFAULT "",
			tvdb_json TEXT NOT NULL DEFAULT "",
			tvmaze_json TEXT NOT NULL DEFAULT "",
			anilist_json TEXT NOT NULL DEFAULT "",
			bluray_json TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
	`
	externalMetadataSourcePathIndexDDL = `CREATE INDEX IF NOT EXISTS idx_external_metadata_source_path ON external_metadata (source_path)`
)

type migrationStep struct {
	id        string
	dependsOn []string
	apply     func(context.Context, migrationExecutor) error
}

type migrationExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Migration authoring note:
//   - Add new forward-only migrations to migrationRegistry with a stable ID.
//   - Never rename or reuse an existing migration ID after it ships.
//   - Depend on the narrowest prerequisite IDs you need instead of assuming a
//     contiguous global version. That keeps branch-local migrations merge-safe.
//   - When replacing or superseding a branch-local migration before release,
//     update legacyVersionToMigrationIDs only if the old integer user_version
//     bridge mapping also changed historically.
var migrationRegistry = []migrationStep{
	{id: baselineMigrationID, apply: createBaselineSchema},
	{
		id:        "2026_04_add_dvd_mediainfo",
		dependsOn: []string{baselineMigrationID},
		apply:     migrateAddDVDMediaInfo,
	},
	{
		id:        "2026_04_add_release_override_use_season_episode",
		dependsOn: []string{baselineMigrationID},
		apply:     migrateAddReleaseOverrideUseSeasonEpisode,
	},
	{
		id:        "2026_04_add_history_indexes",
		dependsOn: []string{baselineMigrationID},
		apply:     migrateAddHistoryIndexes,
	},
	{
		id:        "2026_04_backfill_uploaded_image_usage_scope",
		dependsOn: []string{"2026_04_add_history_indexes"},
		apply:     migrateBackfillUploadedImageUsageScope,
	},
	{
		id:        "2026_04_add_screenshot_slot_tables",
		dependsOn: []string{"2026_04_backfill_uploaded_image_usage_scope"},
		apply:     migrateAddScreenshotSlotTables,
	},
	{
		id:        "2026_04_normalize_description_overrides",
		dependsOn: []string{"2026_04_add_screenshot_slot_tables"},
		apply:     migrateNormalizeDescriptionOverrides,
	},
	{
		id:        "2026_04_add_tracker_cookies",
		dependsOn: []string{"2026_04_normalize_description_overrides"},
		apply:     migrateAddTrackerCookies,
	},
	{
		id:        "2026_04_add_release_category",
		dependsOn: []string{"2026_04_add_tracker_cookies"},
		apply:     migrateAddReleaseCategory,
	},
	{
		id:        "2026_05_add_bluray_external_metadata",
		dependsOn: []string{"2026_04_add_release_category"},
		apply:     migrateAddBlurayExternalMetadata,
	},
	{
		id:        "2026_06_add_tracker_auth_state",
		dependsOn: []string{"2026_05_add_bluray_external_metadata"},
		apply:     migrateAddTrackerAuthState,
	},
	{
		id:        "2026_07_add_external_ids_mal",
		dependsOn: []string{"2026_06_add_tracker_auth_state"},
		apply:     migrateAddExternalIDsMAL,
	},
	{
		id:        "2026_07_add_anilist_external_metadata",
		dependsOn: []string{"2026_07_add_external_ids_mal"},
		apply:     migrateAddAniListExternalMetadata,
	},
	{
		id:        "2026_07_add_tracker_rule_failure_severity",
		dependsOn: []string{"2026_07_add_anilist_external_metadata"},
		apply:     migrateAddTrackerRuleFailureSeverity,
	},
	{
		id:        "2026_07_add_canonical_release_generations",
		dependsOn: []string{"2026_07_add_tracker_rule_failure_severity"},
		apply:     migrateAddCanonicalReleaseGenerations,
	},
	{
		id:        "2026_07_add_tracker_rule_disposition",
		dependsOn: []string{"2026_07_add_canonical_release_generations"},
		apply:     migrateAddTrackerRuleDisposition,
	},
}

// migrateAddTrackerRuleDisposition adds exact enforcement and authorization
// state while preserving readable legacy severity rows.
func migrateAddTrackerRuleDisposition(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "tracker_rule_failures")
	if err != nil {
		return err
	}
	if !exists {
		statements := []string{`CREATE TABLE tracker_rule_failures (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_path TEXT NOT NULL,
			tracker TEXT NOT NULL,
			rule TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT "",
			severity TEXT NOT NULL DEFAULT "blocking",
			disposition TEXT NOT NULL DEFAULT "waivable",
			authorized INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
			`CREATE INDEX IF NOT EXISTS idx_tracker_rule_failures_source_path ON tracker_rule_failures (source_path)`,
			`CREATE INDEX IF NOT EXISTS idx_tracker_rule_failures_tracker ON tracker_rule_failures (tracker)`,
		}
		for _, statement := range statements {
			if _, err := exec.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("db: add tracker rule disposition table: %w", err)
			}
		}
		return nil
	}
	hasDisposition, err := tableColumnExists(ctx, exec, "tracker_rule_failures", "disposition")
	if err != nil {
		return err
	}
	if !hasDisposition {
		if _, err := exec.ExecContext(ctx, `ALTER TABLE tracker_rule_failures ADD COLUMN disposition TEXT NOT NULL DEFAULT "waivable"`); err != nil {
			return fmt.Errorf("db: add tracker rule disposition: %w", err)
		}
	}
	hasAuthorized, err := tableColumnExists(ctx, exec, "tracker_rule_failures", "authorized")
	if err != nil {
		return err
	}
	if !hasAuthorized {
		if _, err := exec.ExecContext(ctx, `ALTER TABLE tracker_rule_failures ADD COLUMN authorized INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("db: add tracker rule authorization: %w", err)
		}
	}
	if _, err := exec.ExecContext(ctx, `UPDATE tracker_rule_failures SET disposition = CASE
		WHEN severity = "warning" THEN "advisory"
		WHEN severity = "blocking" OR severity = "" THEN "waivable"
		ELSE "strict"
	END`); err != nil {
		return fmt.Errorf("db: backfill tracker rule disposition: %w", err)
	}
	return nil
}

// migrateAddCanonicalReleaseGenerations is the branch's single first-time
// migration from the last public schema to canonical prepared generations.
func migrateAddCanonicalReleaseGenerations(ctx context.Context, exec migrationExecutor) error {
	if _, err := exec.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS prepared_release_current (
			source_path TEXT PRIMARY KEY,
			generation INTEGER NOT NULL,
			source_fingerprint TEXT NOT NULL,
			fact_instruction_fingerprint TEXT NOT NULL,
			policy_fingerprint TEXT NOT NULL,
			contract_version TEXT NOT NULL,
			source_json TEXT NOT NULL,
			naming_json TEXT NOT NULL,
			episode_json TEXT NOT NULL,
			media_json TEXT NOT NULL,
			disc_json TEXT NOT NULL,
			assessments_json TEXT NOT NULL,
			prepared_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("db: create prepared release current: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_prepared_release_current_generation
		ON prepared_release_current (source_path, generation)
	`); err != nil {
		return fmt.Errorf("db: index prepared release current: %w", err)
	}

	if err := ensurePreparedExternalIDsSchema(ctx, exec); err != nil {
		return err
	}
	if err := ensurePreparedExternalMetadataSchema(ctx, exec); err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, `
		UPDATE external_ids SET
			source_tmdb = CASE WHEN tmdb_id > 0 AND source_tmdb = "" THEN "legacy" ELSE source_tmdb END,
			source_imdb = CASE WHEN imdb_id > 0 AND source_imdb = "" THEN "legacy" ELSE source_imdb END,
			source_tvdb = CASE WHEN tvdb_id > 0 AND source_tvdb = "" THEN "legacy" ELSE source_tvdb END,
			source_tvmaze = CASE WHEN tvmaze_id > 0 AND source_tvmaze = "" THEN "legacy" ELSE source_tvmaze END,
			source_mal = CASE WHEN mal_id > 0 AND source_mal = "" THEN "legacy" ELSE source_mal END,
			category_provenance = CASE WHEN category != "" AND category_provenance = "" THEN "legacy" ELSE category_provenance END,
			contract_version = CASE WHEN contract_version = "" THEN "legacy" ELSE contract_version END
	`); err != nil {
		return fmt.Errorf("db: seed legacy external identity lineage: %w", err)
	}
	return nil
}

func ensurePreparedExternalIDsSchema(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "external_ids")
	if err != nil {
		return fmt.Errorf("db: inspect external_ids for prepared release: %w", err)
	}
	if !exists {
		if _, err := exec.ExecContext(ctx, `
			CREATE TABLE external_ids (
				source_path TEXT PRIMARY KEY,
				generation INTEGER NOT NULL DEFAULT 0,
				tmdb_id INTEGER NOT NULL DEFAULT 0,
				imdb_id INTEGER NOT NULL DEFAULT 0,
				tvdb_id INTEGER NOT NULL DEFAULT 0,
				tvmaze_id INTEGER NOT NULL DEFAULT 0,
				mal_id INTEGER NOT NULL DEFAULT 0,
				category TEXT NOT NULL DEFAULT "",
				source_tmdb TEXT NOT NULL DEFAULT "",
				source_imdb TEXT NOT NULL DEFAULT "",
				source_tvdb TEXT NOT NULL DEFAULT "",
				source_tvmaze TEXT NOT NULL DEFAULT "",
				source_mal TEXT NOT NULL DEFAULT "",
				category_provenance TEXT NOT NULL DEFAULT "",
				override_json TEXT NOT NULL DEFAULT "{}",
				conflict_status TEXT NOT NULL DEFAULT "none",
				source_fingerprint TEXT NOT NULL DEFAULT "",
				intent_fingerprint TEXT NOT NULL DEFAULT "",
				contract_version TEXT NOT NULL DEFAULT "legacy",
				resolved_at TEXT NOT NULL DEFAULT "",
				updated_at TEXT NOT NULL
			)
		`); err != nil {
			return fmt.Errorf("db: create external_ids for prepared release: %w", err)
		}
		return nil
	}

	columns := []struct {
		name string
		ddl  string
	}{
		{name: "generation", ddl: `ALTER TABLE external_ids ADD COLUMN generation INTEGER NOT NULL DEFAULT 0`},
		{name: "category_provenance", ddl: `ALTER TABLE external_ids ADD COLUMN category_provenance TEXT NOT NULL DEFAULT ""`},
		{name: "override_json", ddl: `ALTER TABLE external_ids ADD COLUMN override_json TEXT NOT NULL DEFAULT "{}"`},
		{name: "conflict_status", ddl: `ALTER TABLE external_ids ADD COLUMN conflict_status TEXT NOT NULL DEFAULT "none"`},
		{name: "source_fingerprint", ddl: `ALTER TABLE external_ids ADD COLUMN source_fingerprint TEXT NOT NULL DEFAULT ""`},
		{name: "intent_fingerprint", ddl: `ALTER TABLE external_ids ADD COLUMN intent_fingerprint TEXT NOT NULL DEFAULT ""`},
		{name: "contract_version", ddl: `ALTER TABLE external_ids ADD COLUMN contract_version TEXT NOT NULL DEFAULT "legacy"`},
		{name: "resolved_at", ddl: `ALTER TABLE external_ids ADD COLUMN resolved_at TEXT NOT NULL DEFAULT ""`},
	}
	for _, column := range columns {
		columnExists, err := tableColumnExists(ctx, exec, "external_ids", column.name)
		if err != nil {
			return fmt.Errorf("db: inspect external_ids.%s: %w", column.name, err)
		}
		if columnExists {
			continue
		}
		if _, err := exec.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("db: add external_ids.%s: %w", column.name, err)
		}
	}
	return nil
}

func ensurePreparedExternalMetadataSchema(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "external_metadata")
	if err != nil {
		return fmt.Errorf("db: inspect external_metadata for prepared release: %w", err)
	}
	if !exists {
		if err := createExternalMetadataSchema(ctx, exec); err != nil {
			return err
		}
	}
	columnExists, err := tableColumnExists(ctx, exec, "external_metadata", "generation")
	if err != nil {
		return fmt.Errorf("db: inspect external_metadata.generation: %w", err)
	}
	if !columnExists {
		if _, err := exec.ExecContext(ctx, `ALTER TABLE external_metadata ADD COLUMN generation INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("db: add external_metadata.generation: %w", err)
		}
	}
	return nil
}

// migrateAddTrackerRuleFailureSeverity creates the rule-results table when
// absent or adds a fail-closed severity column to the legacy table.
func migrateAddTrackerRuleFailureSeverity(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "tracker_rule_failures")
	if err != nil {
		return err
	}
	if !exists {
		statements := []string{
			`CREATE TABLE tracker_rule_failures (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				source_path TEXT NOT NULL,
				tracker TEXT NOT NULL,
				rule TEXT NOT NULL,
				reason TEXT NOT NULL DEFAULT "",
				severity TEXT NOT NULL DEFAULT "blocking",
				created_at TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_tracker_rule_failures_source_path ON tracker_rule_failures (source_path)`,
			`CREATE INDEX IF NOT EXISTS idx_tracker_rule_failures_tracker ON tracker_rule_failures (tracker)`,
		}
		for _, statement := range statements {
			if _, err := exec.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("db: add tracker rule failure severity: %w", err)
			}
		}
		return nil
	}

	hasSeverity, err := tableColumnExists(ctx, exec, "tracker_rule_failures", "severity")
	if err != nil {
		return err
	}
	if !hasSeverity {
		if _, err := exec.ExecContext(ctx, `ALTER TABLE tracker_rule_failures ADD COLUMN severity TEXT NOT NULL DEFAULT "blocking"`); err != nil {
			return fmt.Errorf("db: add tracker rule failure severity: %w", err)
		}
	}
	return nil
}

var legacyVersionToMigrationIDs = map[int][]string{
	1: {baselineMigrationID},
	2: {baselineMigrationID, "2026_04_add_dvd_mediainfo"},
	3: {baselineMigrationID, "2026_04_add_dvd_mediainfo", "2026_04_add_release_override_use_season_episode"},
	4: {baselineMigrationID, "2026_04_add_dvd_mediainfo", "2026_04_add_release_override_use_season_episode", "2026_04_add_history_indexes"},
	5: {
		baselineMigrationID,
		"2026_04_add_dvd_mediainfo",
		"2026_04_add_release_override_use_season_episode",
		"2026_04_add_history_indexes",
		"2026_04_backfill_uploaded_image_usage_scope",
	},
	6: {
		baselineMigrationID,
		"2026_04_add_dvd_mediainfo",
		"2026_04_add_release_override_use_season_episode",
		"2026_04_add_history_indexes",
		"2026_04_backfill_uploaded_image_usage_scope",
		"2026_04_add_screenshot_slot_tables",
	},
	7: {
		baselineMigrationID,
		"2026_04_add_dvd_mediainfo",
		"2026_04_add_release_override_use_season_episode",
		"2026_04_add_history_indexes",
		"2026_04_backfill_uploaded_image_usage_scope",
		"2026_04_add_screenshot_slot_tables",
		"2026_04_normalize_description_overrides",
	},
	8: {
		baselineMigrationID,
		"2026_04_add_dvd_mediainfo",
		"2026_04_add_release_override_use_season_episode",
		"2026_04_add_history_indexes",
		"2026_04_backfill_uploaded_image_usage_scope",
		"2026_04_add_screenshot_slot_tables",
		"2026_04_normalize_description_overrides",
		"2026_04_add_tracker_cookies",
	},
}

func migrateAddDVDMediaInfo(ctx context.Context, exec migrationExecutor) error {
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
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddReleaseOverrideUseSeasonEpisode(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`ALTER TABLE release_overrides ADD COLUMN use_season_episode INTEGER`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddHistoryIndexes(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_file_metadata_updated_at ON file_metadata(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_records_source_created ON upload_records(source_path, created_at DESC)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateBackfillUploadedImageUsageScope(ctx context.Context, exec migrationExecutor) error {
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
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddScreenshotSlotTables(ctx context.Context, exec migrationExecutor) error {
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
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateNormalizeDescriptionOverrides(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "description_overrides")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := exec.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS description_overrides (
				source_path TEXT NOT NULL,
				group_key TEXT NOT NULL DEFAULT "",
				description TEXT NOT NULL DEFAULT "",
				updated_at TEXT NOT NULL,
				PRIMARY KEY (source_path, group_key)
			)
		`); err != nil {
			return fmt.Errorf("db: %w", err)
		}
		if _, err := exec.ExecContext(
			ctx,
			`CREATE INDEX IF NOT EXISTS idx_description_overrides_source_path ON description_overrides (source_path)`,
		); err != nil {
			return fmt.Errorf("db: %w", err)
		}
		return nil
	}

	statements := []string{
		`ALTER TABLE description_overrides RENAME TO description_overrides_legacy`,
		`
		CREATE TABLE description_overrides (
			source_path TEXT NOT NULL,
			group_key TEXT NOT NULL DEFAULT "",
			description TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL,
			PRIMARY KEY (source_path, group_key)
		)
		`,
		`
		INSERT OR REPLACE INTO description_overrides (source_path, group_key, description, updated_at)
		SELECT
			TRIM(COALESCE(source_path, "")),
			"",
			COALESCE(description, ""),
			CASE
				WHEN TRIM(COALESCE(updated_at, "")) = "" THEN STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')
				ELSE updated_at
			END
		FROM description_overrides_legacy
		WHERE TRIM(COALESCE(source_path, "")) <> ""
		ORDER BY rowid
		`,
		`DROP TABLE description_overrides_legacy`,
		`CREATE INDEX IF NOT EXISTS idx_description_overrides_source_path ON description_overrides (source_path)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddTrackerCookies(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`
		CREATE TABLE IF NOT EXISTS tracker_cookies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tracker_id TEXT NOT NULL,
			cookie_name TEXT NOT NULL,
			encrypted_value TEXT NOT NULL,
			nonce TEXT NOT NULL,
			auth_tag TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(tracker_id, cookie_name)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_cookies_tracker_id ON tracker_cookies (tracker_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_cookies_created_at ON tracker_cookies (created_at)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddReleaseCategory(ctx context.Context, exec migrationExecutor) error {
	tablePresent, err := tableExists(ctx, exec, "file_metadata")
	if err != nil {
		return err
	}
	if !tablePresent {
		return nil
	}
	exists, err := tableColumnExists(ctx, exec, "file_metadata", "release_category")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `ALTER TABLE file_metadata ADD COLUMN release_category TEXT NOT NULL DEFAULT ""`); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	return nil
}

func migrateAddBlurayExternalMetadata(ctx context.Context, exec migrationExecutor) error {
	tablePresent, err := tableExists(ctx, exec, "external_metadata")
	if err != nil {
		return err
	}
	if !tablePresent {
		return nil
	}
	exists, err := tableColumnExists(ctx, exec, "external_metadata", "bluray_json")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `ALTER TABLE external_metadata ADD COLUMN bluray_json TEXT NOT NULL DEFAULT ""`); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	return nil
}

// migrateAddAniListExternalMetadata adds the optional rich AniList metadata
// column when the external metadata table exists in the current schema.
func migrateAddAniListExternalMetadata(ctx context.Context, exec migrationExecutor) error {
	tablePresent, err := tableExists(ctx, exec, "external_metadata")
	if err != nil {
		return err
	}
	if !tablePresent {
		if err := createExternalMetadataSchema(ctx, exec); err != nil {
			return err
		}
		return nil
	}
	exists, err := tableColumnExists(ctx, exec, "external_metadata", "anilist_json")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `ALTER TABLE external_metadata ADD COLUMN anilist_json TEXT NOT NULL DEFAULT ""`); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	return nil
}

func createExternalMetadataSchema(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		externalMetadataTableDDL,
		externalMetadataSourcePathIndexDDL,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddTrackerAuthState(ctx context.Context, exec migrationExecutor) error {
	statements := []string{
		`
		CREATE TABLE IF NOT EXISTS tracker_auth_state (
			tracker_id TEXT NOT NULL,
			state_key TEXT NOT NULL,
			encrypted_value TEXT NOT NULL,
			nonce TEXT NOT NULL,
			auth_tag TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (tracker_id, state_key)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_auth_state_tracker_id ON tracker_auth_state (tracker_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tracker_auth_state_updated_at ON tracker_auth_state (updated_at)`,
	}

	for _, statement := range statements {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db: %w", err)
		}
	}

	return nil
}

func migrateAddExternalIDsMAL(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "external_ids")
	if err != nil {
		return fmt.Errorf("db: inspect external_ids table: %w", err)
	}
	if !exists {
		statements := []string{
			`
			CREATE TABLE IF NOT EXISTS external_ids (
				source_path TEXT PRIMARY KEY,
				tmdb_id INTEGER NOT NULL DEFAULT 0,
				imdb_id INTEGER NOT NULL DEFAULT 0,
				tvdb_id INTEGER NOT NULL DEFAULT 0,
				tvmaze_id INTEGER NOT NULL DEFAULT 0,
				mal_id INTEGER NOT NULL DEFAULT 0,
				category TEXT NOT NULL DEFAULT "",
				source_tmdb TEXT NOT NULL DEFAULT "",
				source_imdb TEXT NOT NULL DEFAULT "",
				source_tvdb TEXT NOT NULL DEFAULT "",
				source_tvmaze TEXT NOT NULL DEFAULT "",
				source_mal TEXT NOT NULL DEFAULT "",
				updated_at TEXT NOT NULL
			)
			`,
			`CREATE INDEX IF NOT EXISTS idx_external_ids_source_path ON external_ids (source_path)`,
		}
		for _, statement := range statements {
			if _, err := exec.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("db: create external_ids for mal migration: %w", err)
			}
		}
		return nil
	}

	columns := []struct {
		name string
		ddl  string
	}{
		{name: "mal_id", ddl: `ALTER TABLE external_ids ADD COLUMN mal_id INTEGER NOT NULL DEFAULT 0`},
		{name: "source_mal", ddl: `ALTER TABLE external_ids ADD COLUMN source_mal TEXT NOT NULL DEFAULT ""`},
	}
	for _, column := range columns {
		exists, err := tableColumnExists(ctx, exec, "external_ids", column.name)
		if err != nil {
			return fmt.Errorf("db: inspect external_ids.%s: %w", column.name, err)
		}
		if exists {
			continue
		}
		if _, err := exec.ExecContext(ctx, column.ddl); err != nil {
			return fmt.Errorf("db: add external_ids.%s: %w", column.name, err)
		}
	}
	if err := backfillExternalIDsMALFromMetadata(ctx, exec); err != nil {
		return err
	}
	return nil
}

func backfillExternalIDsMALFromMetadata(ctx context.Context, exec migrationExecutor) error {
	exists, err := tableExists(ctx, exec, "external_metadata")
	if err != nil {
		return fmt.Errorf("db: inspect external_metadata table: %w", err)
	}
	if !exists {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `
		UPDATE external_ids
		SET
			mal_id = (
				SELECT CAST(json_extract(external_metadata.tmdb_json, '$.MALID') AS INTEGER)
				FROM external_metadata
				WHERE external_metadata.source_path = external_ids.source_path
			),
			source_mal = "tmdb"
		WHERE
			mal_id = 0
			AND EXISTS (
				SELECT 1
				FROM external_metadata
				WHERE
					external_metadata.source_path = external_ids.source_path
					AND json_valid(external_metadata.tmdb_json)
					AND CAST(json_extract(external_metadata.tmdb_json, '$.MALID') AS INTEGER) > 0
			)
	`); err != nil {
		return fmt.Errorf("db: backfill external_ids mal from metadata: %w", err)
	}
	return nil
}

func tableColumnExists(ctx context.Context, exec migrationExecutor, tableName string, columnName string) (bool, error) {
	rows, err := exec.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, fmt.Errorf("db: %w", err)
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
			return false, fmt.Errorf("scan column metadata for table %q: %w", tableName, err)
		}
		if strings.EqualFold(name, columnName) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate column metadata for table %q: %w", tableName, err)
	}
	return false, nil
}

func tableExists(ctx context.Context, exec migrationExecutor, tableName string) (bool, error) {
	var count int
	if err := exec.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, tableName).Scan(&count); err != nil {
		return false, fmt.Errorf("check table %q exists: %w", tableName, err)
	}
	return count > 0, nil
}

// Migrate applies the registered migration graph using a background context.
func Migrate(db *sql.DB) error {
	return MigrateContext(context.Background(), db)
}

// MigrateContext validates and applies the registered forward-only migration
// graph in one BEGIN IMMEDIATE transaction. It bridges a recognized legacy
// user_version only when no migration IDs are recorded, then leaves the legacy
// compatibility version at 8 after all pending steps succeed.
func MigrateContext(ctx context.Context, db *sql.DB) error {
	return migrateContextWithRegistry(ctx, db, migrationRegistry)
}

func migrateContextWithRegistry(ctx context.Context, db *sql.DB, registry []migrationStep) error {
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

	validatedRegistry, err := validatedMigrationRegistry(registry)
	if err != nil {
		return err
	}

	if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
		return err
	}

	applied, err := readAppliedMigrationIDs(ctx, conn)
	if err != nil {
		return err
	}

	if len(applied) == 0 {
		applied, err = bridgeLegacyMigrationState(ctx, conn, validatedRegistry)
		if err != nil {
			return err
		}
	}

	if err := validateAppliedMigrationDependencies(applied, validatedRegistry); err != nil {
		return err
	}

	if err := applyPendingMigrations(ctx, conn, validatedRegistry, applied); err != nil {
		return err
	}
	if err := writeLegacyCompatibilityVersion(ctx, conn); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("db migrate commit: %w", err)
	}
	committed = true

	return nil
}

func validatedMigrationRegistry(steps []migrationStep) ([]migrationStep, error) {
	if len(steps) == 0 {
		return nil, errors.New("db migrate: migration registry is empty")
	}

	steps = slices.Clone(steps)
	stepsByID := make(map[string]migrationStep, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step.id) == "" {
			return nil, errors.New("db migrate: migration id cannot be blank")
		}
		if step.apply == nil {
			return nil, fmt.Errorf("db migrate: nil migration function for id %q", step.id)
		}
		if _, exists := stepsByID[step.id]; exists {
			return nil, fmt.Errorf("db migrate: duplicate migration id %q", step.id)
		}
		stepsByID[step.id] = step
	}

	for _, step := range steps {
		for _, depID := range step.dependsOn {
			if _, ok := stepsByID[depID]; !ok {
				return nil, fmt.Errorf("db migrate: migration %q depends on unknown migration %q", step.id, depID)
			}
		}
	}

	seen := make(map[string]bool, len(steps))
	inStack := make(map[string]bool, len(steps))
	var visit func(string) error
	visit = func(id string) error {
		if inStack[id] {
			return fmt.Errorf("db migrate: dependency cycle detected at migration %q", id)
		}
		if seen[id] {
			return nil
		}
		inStack[id] = true
		step := stepsByID[id]
		for _, depID := range step.dependsOn {
			if err := visit(depID); err != nil {
				return err
			}
		}
		inStack[id] = false
		seen[id] = true
		return nil
	}

	for _, step := range steps {
		if err := visit(step.id); err != nil {
			return nil, err
		}
	}

	return steps, nil
}

func ensureSchemaMigrationsTable(ctx context.Context, exec migrationExecutor) error {
	if _, err := exec.ExecContext(ctx, schemaMigrationsTableDDL); err != nil {
		return fmt.Errorf("db migrate: ensure schema_migrations table: %w", err)
	}
	return nil
}

func readAppliedMigrationIDs(ctx context.Context, exec migrationExecutor) (map[string]struct{}, error) {
	rows, err := exec.QueryContext(ctx, `SELECT id FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db migrate: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("db migrate: scan schema_migrations: %w", err)
		}
		applied[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db migrate: iterate schema_migrations: %w", err)
	}
	return applied, nil
}

func bridgeLegacyMigrationState(ctx context.Context, exec migrationExecutor, registry []migrationStep) (map[string]struct{}, error) {
	currentVersion, err := readUserVersion(ctx, exec)
	if err != nil {
		return nil, err
	}

	if currentVersion == 0 {
		return map[string]struct{}{}, nil
	}

	legacyIDs, ok := legacyVersionToMigrationIDs[currentVersion]
	if !ok {
		return nil, fmt.Errorf("db migrate: unsupported legacy schema version %d for schema_migrations bridge", currentVersion)
	}

	registryIDs := make(map[string]struct{}, len(registry))
	for _, step := range registry {
		registryIDs[step.id] = struct{}{}
	}

	applied := make(map[string]struct{}, len(legacyIDs))
	for _, id := range legacyIDs {
		if _, ok := registryIDs[id]; !ok {
			return nil, fmt.Errorf("db migrate: legacy migration %q is not registered", id)
		}
		if err := recordAppliedMigration(ctx, exec, id); err != nil {
			return nil, err
		}
		applied[id] = struct{}{}
	}

	return applied, nil
}

func validateAppliedMigrationDependencies(applied map[string]struct{}, registry []migrationStep) error {
	stepsByID := make(map[string]migrationStep, len(registry))
	for _, step := range registry {
		stepsByID[step.id] = step
	}

	for appliedID := range applied {
		step, known := stepsByID[appliedID]
		if !known {
			continue
		}
		for _, depID := range step.dependsOn {
			if _, ok := applied[depID]; !ok {
				return fmt.Errorf("db migrate: applied migration %q is missing dependency %q", appliedID, depID)
			}
		}
	}

	return nil
}

func applyPendingMigrations(ctx context.Context, exec migrationExecutor, registry []migrationStep, applied map[string]struct{}) error {
	for {
		progress := false
		pendingKnown := make([]string, 0)

		for _, step := range registry {
			if _, ok := applied[step.id]; ok {
				continue
			}
			pendingKnown = append(pendingKnown, step.id)

			ready := true
			for _, depID := range step.dependsOn {
				if _, ok := applied[depID]; !ok {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}

			if err := step.apply(ctx, exec); err != nil {
				return fmt.Errorf("db migrate %s: %w", step.id, err)
			}
			if err := recordAppliedMigration(ctx, exec, step.id); err != nil {
				return err
			}
			applied[step.id] = struct{}{}
			progress = true
		}

		if len(pendingKnown) == 0 {
			return nil
		}
		if progress {
			continue
		}
		return fmt.Errorf("db migrate: pending migrations have unmet dependencies: %s", strings.Join(pendingKnown, ", "))
	}
}

func recordAppliedMigration(ctx context.Context, exec migrationExecutor, id string) error {
	appliedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := exec.ExecContext(ctx, `
		INSERT OR IGNORE INTO schema_migrations (id, applied_at)
		VALUES (?, ?)
	`, id, appliedAt); err != nil {
		return fmt.Errorf("db migrate: record applied migration %q: %w", id, err)
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

func writeLegacyCompatibilityVersion(ctx context.Context, exec migrationExecutor) error {
	currentVersion, err := readUserVersion(ctx, exec)
	if err != nil {
		return err
	}
	if currentVersion == legacyCompatibilitySchemaVersion {
		return nil
	}
	if err := writeUserVersion(ctx, exec, legacyCompatibilitySchemaVersion); err != nil {
		return err
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
			mal_id INTEGER NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT "",
			source_tmdb TEXT NOT NULL DEFAULT "",
			source_imdb TEXT NOT NULL DEFAULT "",
			source_tvdb TEXT NOT NULL DEFAULT "",
			source_tvmaze TEXT NOT NULL DEFAULT "",
			source_mal TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL
		)
		`,
		externalMetadataTableDDL,
		`CREATE INDEX IF NOT EXISTS idx_external_ids_source_path ON external_ids (source_path)`,
		externalMetadataSourcePathIndexDDL,
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
			source_path TEXT NOT NULL,
			group_key TEXT NOT NULL DEFAULT "",
			description TEXT NOT NULL DEFAULT "",
			updated_at TEXT NOT NULL,
			PRIMARY KEY (source_path, group_key)
		)
		`,
		`CREATE INDEX IF NOT EXISTS idx_description_overrides_source_path ON description_overrides (source_path)`,
		`
		CREATE TABLE IF NOT EXISTS tracker_rule_failures (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_path TEXT NOT NULL,
			tracker TEXT NOT NULL,
			rule TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT "",
			severity TEXT NOT NULL DEFAULT "blocking",
			disposition TEXT NOT NULL DEFAULT "waivable",
			authorized INTEGER NOT NULL DEFAULT 0,
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
