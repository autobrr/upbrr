// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
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
