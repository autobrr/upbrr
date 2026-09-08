// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// BackupReadOnly takes a consistent online SQLite backup without application
// bootstrap, migrations, journal-mode changes, or writes to the source DB.
// The destination must not exist and its parent must already be private.
func BackupReadOnly(ctx context.Context, sourcePath, destinationPath string) error {
	if ctx == nil {
		return errors.New("snapshot context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	sourcePath, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("snapshot source path: %w", err)
	}
	uriPath := filepath.ToSlash(sourcePath)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath // URI syntax requires a leading slash before Windows drive letters.
	}
	sourceURI := url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: "mode=ro",
	}
	source, err := sql.Open("sqlite", sourceURI.String())
	if err != nil {
		return fmt.Errorf("snapshot open source: %w", err)
	}
	defer source.Close()
	reserved, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("snapshot reserve destination: %w", err)
	}
	if err := reserved.Close(); err != nil {
		return fmt.Errorf("snapshot close destination: %w", err)
	}
	conn, err := source.Conn(ctx)
	if err != nil {
		return fmt.Errorf("snapshot source connection: %w", err)
	}
	defer conn.Close()
	err = conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite driver does not support consistent backup")
		}
		backup, err := backuper.NewBackup(destinationPath)
		if err != nil {
			return fmt.Errorf("snapshot initialize backup: %w", err)
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = backup.Finish()
				return fmt.Errorf("snapshot canceled: %w", err)
			}
			more, err := backup.Step(128)
			if err != nil {
				_ = backup.Finish()
				return fmt.Errorf("snapshot copy pages: %w", err)
			}
			if !more {
				break
			}
		}
		return backup.Finish()
	})
	if err != nil {
		return fmt.Errorf("snapshot backup: %w", err)
	}
	return nil
}

var liveTestDiscardTables = []string{
	"release_workflow_work", "release_workflow_effects", "release_workflow_events", "release_workflow_continuations",
	"release_workflow_intents", "release_workflow_operations", "release_workflow_states", "description_overrides",
	"dvd_mediainfo", "external_ids", "external_metadata", "file_metadata", "playlist_selections", "prepared_release_current",
	"release_overrides", "screenshot_final_selections", "screenshot_slot_variants", "screenshot_slots", "screenshots",
	"tracker_metadata", "tracker_rule_failures", "tracker_timestamps", "upload_records", "uploaded_images", "ui_states",
}

var liveTestConfigSections = []string{
	"MainSettings", "ImageHosting", "Metadata", "ScreenshotHandling", "Description", "ClientSetup", "ArrIntegration",
	"TorrentCreation", "PostUpload", "Logging", "Trackers", "TorrentClients", "cookies_encryption_salt", "cookies_encryption_auth_state",
}

// These historical branch migrations only modify domains that profile pruning
// empties. They never alter retained configuration or tracker authentication.
// Keep this compatibility list local to isolation; do not replay or rename IDs.
var liveTestHistoricalDiscardOnlyMigrations = []string{
	// 9c9afd0ac/d8f4d692d: prepared_release_current and external identity/metadata lineage.
	"2026_07_add_prepared_release_generations",
	// 7a302a328: playlist_selections.source_fingerprint.
	"2026_08_add_multi_disc_media_binding",
	// beb21cc45: fingerprint/generation/disc columns on the five screenshot/image tables.
	"2026_08_bind_prepared_media_assets",
}

// ValidateLiveTestSchema accepts current and audited historical discard-only
// migrations, rejecting unknown migrations and unknown populated state.
// Run it before clone migrations as well as in the pruning transaction.
func (r *SQLiteRepository) ValidateLiveTestSchema(ctx context.Context) error {
	return validateLiveTestSchema(ctx, r.db)
}

func validateLiveTestSchema(ctx context.Context, exec migrationExecutor) error {
	var integrity string
	if err := exec.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fmt.Errorf("snapshot integrity check: %w", err)
	}
	if integrity != "ok" {
		return errors.New("snapshot database integrity check failed")
	}
	var triggers int
	if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'").Scan(&triggers); err != nil {
		return fmt.Errorf("snapshot inspect triggers: %w", err)
	}
	if triggers != 0 {
		return errors.New("snapshot contains unsupported application triggers")
	}
	var version int
	if err := exec.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("snapshot schema version: %w", err)
	}
	if version > legacyCompatibilitySchemaVersion {
		return errors.New("snapshot has a newer legacy schema")
	}
	if exists, err := tableExists(ctx, exec, "schema_migrations"); err != nil {
		return err
	} else if exists {
		ids, err := liveTestStrings(ctx, exec, "SELECT id FROM schema_migrations")
		if err != nil {
			return err
		}
		for _, id := range ids {
			if slices.Contains(liveTestHistoricalDiscardOnlyMigrations, id) {
				continue
			}
			if !slices.ContainsFunc(migrationRegistry, func(m migrationStep) bool { return m.id == id }) {
				return errors.New("snapshot has an unsupported migration")
			}
		}
	}
	names, err := liveTestStrings(ctx, exec, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return err
	}
	for _, name := range names {
		if strings.HasPrefix(name, "sqlite_") || slices.Contains(liveTestDiscardTables, name) ||
			slices.Contains([]string{"schema_migrations", "config_settings", "tracker_cookies", "tracker_auth_state"}, name) {
			continue
		}
		var count int
		if err := exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`).Scan(&count); err != nil {
			return fmt.Errorf("snapshot inspect unknown table: %w", err)
		}
		if count != 0 {
			return errors.New("snapshot contains an unknown populated application table")
		}
	}
	if slices.Contains(names, "config_settings") {
		sections, err := liveTestStrings(ctx, exec, "SELECT section FROM config_settings")
		if err != nil {
			return err
		}
		for _, section := range sections {
			if !slices.Contains(liveTestConfigSections, section) {
				return errors.New("snapshot contains an unknown config section")
			}
		}
	}
	return nil
}

func liveTestStrings(ctx context.Context, exec migrationExecutor, query string) ([]string, error) {
	rows, err := exec.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("snapshot inspect schema: %w", err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("snapshot read schema: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snapshot schema rows: %w", err)
	}
	return values, nil
}

// PruneLiveTestState transactionally clears every known production authority
// domain while preserving configuration and encrypted tracker authentication.
func (r *SQLiteRepository) PruneLiveTestState(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("snapshot begin pruning: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateLiveTestSchema(ctx, tx); err != nil {
		return err
	}
	sequenceExists, err := tableExists(ctx, tx, "sqlite_sequence")
	if err != nil {
		return err
	}
	for _, name := range liveTestDiscardTables {
		exists, err := tableExists(ctx, tx, name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		//nolint:gosec // Table identifiers come exclusively from the fixed isolation domain list.
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+name+`"`); err != nil {
			return fmt.Errorf("snapshot clear domain %s: %w", name, err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+name+`"`).Scan(&count); err != nil {
			return fmt.Errorf("snapshot verify domain: %w", err)
		}
		if count != 0 {
			return errors.New("snapshot domain remained populated")
		}
		if sequenceExists {
			if _, err := tx.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name = ?", name); err != nil {
				return fmt.Errorf("snapshot clear sequence: %w", err)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("snapshot foreign key check: %w", err)
	}
	defer rows.Close()
	invalid := rows.Next()
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return fmt.Errorf("snapshot foreign key rows: %w", rowErr)
	}
	if invalid {
		return errors.New("snapshot has invalid foreign keys")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("snapshot commit pruning: %w", err)
	}
	return nil
}
