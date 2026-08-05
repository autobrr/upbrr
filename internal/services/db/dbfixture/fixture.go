// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package dbfixture provides SQLite fixtures for integration tests outside the
// db package.
package dbfixture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
)

var (
	templateOnce    sync.Once
	templatePayload []byte
	errTemplate     error
)

// WriteMigrated writes a private SQLite database with the current empty
// schema. The migration graph is materialized once per test process.
func WriteMigrated(t testing.TB, dbPath string) {
	t.Helper()

	templateOnce.Do(func() {
		templatePayload, errTemplate = buildTemplate()
	})
	if errTemplate != nil {
		t.Fatalf("prepare migrated database template: %v", errTemplate)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create migrated database parent: %v", err)
	}
	if err := os.WriteFile(dbPath, templatePayload, 0o600); err != nil {
		t.Fatalf("write migrated database fixture: %v", err)
	}
}

func buildTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "upbrr-db-fixture-")
	if err != nil {
		return nil, fmt.Errorf("create database fixture directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()

	dbPath := filepath.Join(dir, "template.db")
	ctx := context.Background()
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database fixture: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("migrate database fixture: %w", err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = repo.Close()
		return nil, fmt.Errorf("checkpoint database fixture: %w", err)
	}
	if err := repo.Close(); err != nil {
		return nil, fmt.Errorf("close database fixture: %w", err)
	}

	payload, err := os.ReadFile(dbPath)
	if err != nil {
		return nil, fmt.Errorf("read database fixture: %w", err)
	}
	return payload, nil
}
