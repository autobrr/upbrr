// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package authmaterial

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadFromDBPathMissingFileReturnsUnavailable(t *testing.T) {
	t.Parallel()

	_, err := LoadFromDBPath(filepath.Join(t.TempDir(), "state.db"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestLoadFromDBPathRejectsInsecurePermissions(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced consistently on Windows")
	}

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state.db")
	authPath := filepath.Join(tempDir, WebAuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"username":"tester","password_hash":"hash"}`), 0o644); err != nil {
		t.Fatalf("write web auth file: %v", err)
	}
	if err := os.Chmod(authPath, 0o644); err != nil {
		t.Fatalf("chmod web auth file: %v", err)
	}

	_, err := LoadFromDBPath(dbPath)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("expected insecure permissions error, got %v", err)
	}
	if !strings.Contains(err.Error(), authPath) {
		t.Fatalf("expected error to reference %q, got %v", authPath, err)
	}
}

func TestLoadFromDBPathAllowsSecurePermissions(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced consistently on Windows")
	}

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state.db")
	authPath := filepath.Join(tempDir, WebAuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"username":"tester","password_hash":"hash","encryption_key_seed":"seed"}`), 0o600); err != nil {
		t.Fatalf("write web auth file: %v", err)
	}
	if err := os.Chmod(authPath, 0o600); err != nil {
		t.Fatalf("chmod web auth file: %v", err)
	}

	material, err := LoadFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadFromDBPath: %v", err)
	}
	if material.Username != "tester" {
		t.Fatalf("expected username tester, got %q", material.Username)
	}
	if material.PasswordHash != "hash" {
		t.Fatalf("expected password hash hash, got %q", material.PasswordHash)
	}
	if material.EncryptionKeySeed != "seed" {
		t.Fatalf("expected encryption seed seed, got %q", material.EncryptionKeySeed)
	}
}

func TestLoadFromDBPathParsesAllowUnencryptedExport(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "state.db")
	authPath := filepath.Join(tempDir, WebAuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"username":"tester","password_hash":"hash","allow_unencrypted_export":true}`), 0o600); err != nil {
		t.Fatalf("write web auth file: %v", err)
	}

	material, err := LoadFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadFromDBPath: %v", err)
	}
	if !material.AllowUnencryptedExport {
		t.Fatal("expected allow_unencrypted_export to be true")
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Bootstrap("tester", "very-secure-password"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	created, err := store.CreateAPIToken("automation")
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if !strings.HasPrefix(created.Token, "upbrr_"+created.Record.ID+"_") {
		t.Fatalf("unexpected token format: %q", created.Token)
	}
	if strings.Contains(created.Record.Hash, created.Token) {
		t.Fatal("expected stored token hash not to contain raw token")
	}

	status, ok, err := store.VerifyAPIToken(created.Token)
	if err != nil {
		t.Fatalf("VerifyAPIToken: %v", err)
	}
	if !ok {
		t.Fatal("expected token to verify")
	}
	if status.ID != created.Record.ID || status.Name != "automation" {
		t.Fatalf("unexpected status %#v", status)
	}
	if status.LastUsedAt == nil {
		t.Fatal("expected last-used timestamp after verification")
	}

	if err := store.RevokeAPIToken(created.Record.ID); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}
	_, ok, err = store.VerifyAPIToken(created.Token)
	if err != nil {
		t.Fatalf("VerifyAPIToken after revoke: %v", err)
	}
	if ok {
		t.Fatal("expected revoked token to fail verification")
	}
}
