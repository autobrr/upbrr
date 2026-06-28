// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
)

func TestAuthStateRoundTripEncrypted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	if err := SaveAuthState(ctx, dbPath, "ar", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	got, err := LoadAuthState(ctx, dbPath, "AR", "auth_key")
	if err != nil {
		t.Fatalf("LoadAuthState: %v", err)
	}
	if got != "secret-auth-key" {
		t.Fatalf("state value: got %q", got)
	}

	if err := DeleteAuthState(ctx, dbPath, "AR", "auth_key"); err != nil {
		t.Fatalf("DeleteAuthState: %v", err)
	}
	if _, err := LoadAuthState(ctx, dbPath, "AR", "auth_key"); !errors.Is(err, ErrAuthStateNotFound) {
		t.Fatalf("expected ErrAuthStateNotFound, got %v", err)
	}
}

func TestAuthStateRejectsMovedEncryptedPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := SaveAuthState(ctx, dbPath, "AR", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE tracker_auth_state SET tracker_id = ? WHERE tracker_id = ? AND state_key = ?`, "PTP", "AR", "auth_key"); err != nil {
		_ = repo.Close()
		t.Fatalf("move auth state row: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close repo: %v", err)
	}

	if _, err := LoadAuthState(ctx, dbPath, "PTP", "auth_key"); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("expected moved payload identity mismatch, got %v", err)
	}
	if _, err := LoadAuthState(ctx, dbPath, "AR", "auth_key"); !errors.Is(err, ErrAuthStateNotFound) {
		t.Fatalf("expected original key to be missing, got %v", err)
	}
}

func TestAuthStateWriteRequiresWebAuthMaterial(t *testing.T) {
	t.Parallel()

	err := SaveAuthState(context.Background(), filepath.Join(t.TempDir(), "upbrr.db"), "AR", "auth_key", "secret")
	if err == nil {
		t.Fatal("expected missing web auth material error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "auth") {
		t.Fatalf("expected auth material error, got %v", err)
	}
}

func TestDeleteAuthStateDoesNotRequireWebAuthMaterial(t *testing.T) {
	t.Parallel()

	err := DeleteAuthState(context.Background(), filepath.Join(t.TempDir(), "upbrr.db"), "AR", "auth_key")
	if err != nil {
		t.Fatalf("DeleteAuthState: %v", err)
	}
}

func TestDeleteAuthStateRemovesRowsWhenWebAuthMaterialMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := SaveAuthState(ctx, dbPath, "AR", "auth_key", "secret-auth-key"); err != nil {
		t.Fatalf("SaveAuthState: %v", err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(dbPath), authmaterial.WebAuthFileName)); err != nil {
		t.Fatalf("Remove web auth: %v", err)
	}

	if err := DeleteAuthState(ctx, dbPath, "AR", "auth_key"); err != nil {
		t.Fatalf("DeleteAuthState: %v", err)
	}
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	defer func() {
		_ = repo.Close()
	}()
	var count int
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM tracker_auth_state WHERE tracker_id = ? AND state_key = ?`, "AR", "auth_key").Scan(&count); err != nil {
		t.Fatalf("count tracker auth state: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected auth state row deleted, got %d", count)
	}
}
