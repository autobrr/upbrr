// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveTestPruningClearsEveryDomainAndRetainsAuth(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "clone.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	// Use minimal populated domains to make every explicit pruning entry a
	// regression requirement, independently of incidental fixture columns.
	for _, table := range append(append([]string(nil), liveTestDiscardTables...), "tracker_cookies", "tracker_auth_state") {
		if _, err := repo.db.ExecContext(t.Context(), `CREATE TABLE "`+table+`" (id INTEGER PRIMARY KEY AUTOINCREMENT, value TEXT); INSERT INTO "`+table+`" (value) VALUES ('synthetic')`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.db.ExecContext(t.Context(), "CREATE TABLE config_settings(section TEXT PRIMARY KEY, data TEXT); INSERT INTO config_settings VALUES ('MainSettings', '{}'), ('cookies_encryption_salt', 'synthetic-salt')"); err != nil {
		t.Fatal(err)
	}
	if err := repo.PruneLiveTestState(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range liveTestDiscardTables {
		var count int
		if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "`+table+`"`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("domain %s not empty", table)
		}
		if err := repo.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_sequence WHERE name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("sequence %s not empty", table)
		}
	}
	for _, table := range []string{"tracker_cookies", "tracker_auth_state"} {
		var value string
		if err := repo.db.QueryRowContext(t.Context(), `SELECT value FROM "`+table+`" WHERE id = 1`).Scan(&value); err != nil || value != "synthetic" {
			t.Fatalf("retained %s changed: %v", table, err)
		}
		var seq int
		if err := repo.db.QueryRowContext(t.Context(), "SELECT seq FROM sqlite_sequence WHERE name = ?", table).Scan(&seq); err != nil || seq != 1 {
			t.Fatalf("retained sequence changed: %v", err)
		}
	}
}

func TestLiveTestPruningRollsBackOnDomainFailure(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "clone.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.db.ExecContext(t.Context(), `CREATE TABLE release_workflow_work(id INTEGER PRIMARY KEY); INSERT INTO release_workflow_work VALUES (1);
	CREATE TABLE release_workflow_effects(id INTEGER PRIMARY KEY); INSERT INTO release_workflow_effects VALUES(1);
	CREATE TABLE blocker(id INTEGER REFERENCES release_workflow_effects(id)); INSERT INTO blocker VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	// The populated unknown table must fail before any deletion.
	if err := repo.PruneLiveTestState(t.Context()); err == nil {
		t.Fatal("unknown populated state accepted")
	}
	if _, err := repo.db.ExecContext(t.Context(), "DROP TABLE blocker; CREATE TABLE ui_states(id INTEGER REFERENCES release_workflow_effects(id)); INSERT INTO ui_states VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	// A known later domain's restrictive FK fails after an earlier DELETE.
	if err := repo.PruneLiveTestState(t.Context()); err == nil {
		t.Fatal("foreign key violation ignored")
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM release_workflow_work").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("failed pruning was not rolled back")
	}
}

func TestBackupReadOnlyIncludesUncheckpointedWAL(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source.sqlite")
	repo, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.db.ExecContext(t.Context(), "PRAGMA wal_autocheckpoint=0; CREATE TABLE evidence(value TEXT); INSERT INTO evidence VALUES ('synthetic')"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(base, "clone.sqlite")
	if err := BackupReadOnly(t.Context(), source, destination); err != nil {
		t.Fatal(err)
	}
	clone, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	var value string
	if err := clone.db.QueryRowContext(t.Context(), "SELECT value FROM evidence").Scan(&value); err != nil || value != "synthetic" {
		t.Fatalf("WAL snapshot missing: %v", err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("snapshot changed source bytes")
	}
	if err := BackupReadOnly(t.Context(), source, destination); err == nil {
		t.Fatal("existing destination overwritten")
	}
}

func TestLiveTestSchemaRejectsUnknownMigrationAndConfig(t *testing.T) {
	for _, statement := range []string{
		"INSERT INTO schema_migrations VALUES ('future_migration', 'synthetic')",
		"INSERT INTO config_settings VALUES ('future_section', '{}', 'synthetic')",
		"PRAGMA user_version = 999",
		"CREATE TABLE future_authority(value TEXT); INSERT INTO future_authority VALUES ('synthetic')",
		"CREATE TRIGGER future_trigger AFTER DELETE ON screenshots BEGIN SELECT 1; END",
	} {
		repo, err := Open(filepath.Join(t.TempDir(), "clone.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.MigrateContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, id := range liveTestHistoricalDiscardOnlyMigrations {
			if _, err := repo.db.ExecContext(t.Context(), "INSERT INTO schema_migrations VALUES (?, 'synthetic')", id); err != nil {
				t.Fatal(err)
			}
		}
		if err := repo.ValidateLiveTestSchema(t.Context()); err != nil {
			t.Fatalf("audited historical migrations rejected: %v", err)
		}
		if _, err := repo.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
		if err := repo.ValidateLiveTestSchema(t.Context()); err == nil {
			t.Fatal("unsupported schema accepted")
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
