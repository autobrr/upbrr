// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package authmaterial

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreCreateBackupPreservesExactBytesInUniqueSecureFiles(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	authPath := AuthFilePath(dbPath)
	original, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read original auth file: %v", err)
	}

	firstPath, err := store.CreateBackup()
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(firstPath), WebAuthFileName+".backup-") {
		t.Fatalf("unexpected backup name %q", filepath.Base(firstPath))
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("read first backup: %v", err)
	}
	if !bytes.Equal(first, original) {
		t.Fatal("first backup does not preserve exact auth file bytes")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(firstPath)
		if err != nil {
			t.Fatalf("stat first backup: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("backup permissions = %o, want 600", got)
		}
	}

	record, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	record.BrowseRoot = t.TempDir()
	if err := store.UpdateRecord(record); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	updated, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read updated auth file: %v", err)
	}
	secondPath, err := store.CreateBackup()
	if err != nil {
		t.Fatalf("CreateBackup after update: %v", err)
	}
	if secondPath == firstPath {
		t.Fatal("successive backups reused one path")
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("read second backup: %v", err)
	}
	if !bytes.Equal(second, updated) {
		t.Fatal("second backup does not preserve updated auth file bytes")
	}
	first, err = os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("reread first backup: %v", err)
	}
	if !bytes.Equal(first, original) {
		t.Fatal("later backup changed the first backup")
	}
}
