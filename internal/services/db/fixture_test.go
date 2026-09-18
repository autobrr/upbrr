// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Cache only the closed database's bytes, never a connection or a writable file.
// Migration tests must continue to create and migrate their own empty databases.
var migratedTestDatabase = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "upbrr-db-fixture-*")
	if err != nil {
		return nil, fmt.Errorf("create fixture directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fixture.sqlite")
	repo, err := Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fixture: %w", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("migrate fixture: %w", err)
	}
	// As in qui's migrated test fixture, flush the WAL before copying the main file.
	if _, err := repo.RawDB().ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("checkpoint fixture: %w", err)
	}
	if err := repo.Close(); err != nil {
		return nil, fmt.Errorf("close fixture: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	return data, nil
})

func openMigratedTestRepo(t *testing.T) *SQLiteRepository {
	t.Helper()
	data, err := migratedTestDatabase()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.sqlite")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	repo, err := Open(path)
	if err != nil {
		t.Fatalf("open fixture copy: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestMigratedDatabaseFixtureIsolation(t *testing.T) {
	t.Parallel()
	first := openMigratedTestRepo(t)
	if err := first.SaveConfigSection(t.Context(), "fixture-isolation", "first"); err != nil {
		t.Fatal(err)
	}
	second := openMigratedTestRepo(t)
	var loaded string
	if err := second.LoadConfigSection(t.Context(), "fixture-isolation", &loaded); err == nil {
		t.Fatal("new fixture contains another test's data")
	}
	if err := second.SaveConfigSection(t.Context(), "fixture-isolation", "second"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(first.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.LoadConfigSection(t.Context(), "fixture-isolation", &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded != "first" {
		t.Fatalf("reopened value = %q, want first", loaded)
	}
}
