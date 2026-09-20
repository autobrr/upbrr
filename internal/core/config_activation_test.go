// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestConfigActivationGenerationGuardRejectsRetiredRuntime(t *testing.T) {
	ctx := t.Context()
	repo, err := db.Open(filepath.Join(t.TempDir(), "config-activation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InitializeConfigActivationFingerprint(ctx, "initial"); err != nil {
		t.Fatal(err)
	}

	firstRuntimeGuard := configActivationGenerationGuard(repo, 0, "initial")
	if err := firstRuntimeGuard(ctx); err != nil {
		t.Fatalf("initial generation admission: %v", err)
	}

	tx, err := repo.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ActivateConfigTx(ctx, tx, api.ConfigActivation{}, "replacement", []api.ConfigImpactDetail{{Kind: api.ConfigImpactPresentation}}, nil)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatalf("activate second generation: %v", err)
	}

	if err := firstRuntimeGuard(ctx); !errors.Is(err, api.ErrConfigActivationChanged) {
		t.Fatalf("retired runtime admission error = %v", err)
	}
	if err := configActivationGenerationGuard(repo, 1, "replacement")(ctx); err != nil {
		t.Fatalf("current runtime admission: %v", err)
	}
}

func TestConfigActivationGenerationGuardAllowsActiveGenerationWhileReplacementIsPending(t *testing.T) {
	ctx := t.Context()
	repo, err := db.Open(filepath.Join(t.TempDir(), "config-activation-pending.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InitializeConfigActivationFingerprint(ctx, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SavePendingConfigActivation(ctx, "owner", []byte(`{"candidate":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := configActivationGenerationGuard(repo, 0, "active")(ctx); err != nil {
		t.Fatalf("active generation rejected while replacement pending: %v", err)
	}
}
