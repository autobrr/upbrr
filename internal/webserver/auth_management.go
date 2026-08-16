// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/services/db"
)

// ChangeAuthPassword verifies and replaces the persisted WebUI password. The
// caller must stop the WebUI server first so in-memory sessions cannot survive.
// It revokes retained sessions and preserves protected data during legacy key upgrades.
func ChangeAuthPassword(ctx context.Context, dbPath string, currentPassword string, newPassword string) error {
	if ctx == nil {
		return errors.New("change auth password: context is required")
	}
	if strings.TrimSpace(dbPath) == "" {
		return errors.New("change auth password: database path is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("change auth password: %w", err)
	}

	store, err := authmaterial.NewStore(dbPath)
	if err != nil {
		return fmt.Errorf("change auth password: open auth store: %w", err)
	}
	record, err := store.Load()
	if err != nil {
		return fmt.Errorf("change auth password: load auth record: %w", err)
	}
	if !record.AuthMaterial().IsUsable() {
		return errors.New("change auth password: web-auth.json is incomplete")
	}
	if !authmaterial.VerifyPassword(currentPassword, record.PasswordHash) {
		return errors.New("change auth password: current password is incorrect")
	}

	newHash, err := authmaterial.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("change auth password: %w", err)
	}
	if err := clearPersistedAuthSessions(dbPath); err != nil {
		return err
	}

	var repo *db.SQLiteRepository
	openRepo := func() (*db.SQLiteRepository, error) {
		if repo != nil {
			return repo, nil
		}
		opened, err := db.OpenContext(ctx, dbPath)
		if err != nil {
			return nil, fmt.Errorf("change auth password: open database: %w", err)
		}
		if err := opened.MigrateContext(ctx); err != nil {
			_ = opened.Close()
			return nil, fmt.Errorf("change auth password: migrate database: %w", err)
		}
		repo = opened
		return repo, nil
	}
	defer func() {
		if repo != nil {
			_ = repo.Close()
		}
	}()

	if record.PendingUpgrade != nil {
		currentRepo, err := openRepo()
		if err != nil {
			return err
		}
		if err := rewrapProtectedDataForAuthChange(ctx, store, currentRepo, record, record.PendingUpgrade.Target); err != nil {
			return fmt.Errorf("change auth password: resume pending auth update: %w", err)
		}
		record, err = store.FinalizePendingUpgrade(record.Username)
		if err != nil {
			return fmt.Errorf("change auth password: finalize pending auth update: %w", err)
		}
	}

	target := record
	target.PasswordHash = newHash
	target.PendingUpgrade = nil
	if strings.TrimSpace(target.EncryptionKeySeed) == "" {
		target.EncryptionKeySeed, err = authmaterial.GenerateSeed()
		if err != nil {
			return fmt.Errorf("change auth password: generate encryption seed: %w", err)
		}
	}

	_, oldFingerprint, err := record.AuthMaterial().PrimaryHelper()
	if err != nil {
		return fmt.Errorf("change auth password: derive current encryption helper: %w", err)
	}
	_, newFingerprint, err := target.AuthMaterial().PrimaryHelper()
	if err != nil {
		return fmt.Errorf("change auth password: derive replacement encryption helper: %w", err)
	}
	if oldFingerprint == newFingerprint {
		if err := store.UpdatePasswordHash(record.Username, record.PasswordHash, newHash); err != nil {
			return fmt.Errorf("change auth password: persist password: %w", err)
		}
		return nil
	}

	currentRepo, err := openRepo()
	if err != nil {
		return err
	}
	if err := rewrapProtectedDataForAuthChange(ctx, store, currentRepo, record, target); err != nil {
		return fmt.Errorf("change auth password: rewrap protected data: %w", err)
	}
	if _, err := store.FinalizePendingUpgrade(record.Username); err != nil {
		return fmt.Errorf("change auth password: finalize password: %w", err)
	}
	return nil
}

// UpdateBrowseRoots replaces the persisted WebUI browse policy. Paths must be
// existing directories; unrestricted access cannot be combined with roots.
// The caller must stop the WebUI server first to avoid concurrent auth writes.
func UpdateBrowseRoots(ctx context.Context, dbPath string, values []string, allowUnrestricted bool) (int, error) {
	if ctx == nil {
		return 0, errors.New("update browse roots: context is required")
	}
	if strings.TrimSpace(dbPath) == "" {
		return 0, errors.New("update browse roots: database path is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("update browse roots: %w", err)
	}
	if allowUnrestricted && len(values) != 0 {
		return 0, errors.New("update browse roots: unrestricted browsing cannot be combined with browse roots")
	}

	roots, err := normalizeBrowsePolicyRoots(values)
	if err != nil {
		return 0, fmt.Errorf("update browse roots: %w", err)
	}
	if !allowUnrestricted && len(roots) == 0 {
		return 0, errors.New("update browse roots: at least one browse root is required unless unrestricted browsing is explicitly allowed")
	}

	store, err := authmaterial.NewStore(dbPath)
	if err != nil {
		return 0, fmt.Errorf("update browse roots: open auth store: %w", err)
	}
	record, err := store.Load()
	if err != nil {
		return 0, fmt.Errorf("update browse roots: load auth record: %w", err)
	}
	if !record.AuthMaterial().IsUsable() {
		return 0, errors.New("update browse roots: web-auth.json is incomplete")
	}
	record.BrowseRoot = joinBrowsePolicyRoots(roots)
	record.AllowUnrestrictedBrowse = allowUnrestricted
	if record.PendingUpgrade != nil {
		record.PendingUpgrade.Target.BrowseRoot = record.BrowseRoot
		record.PendingUpgrade.Target.AllowUnrestrictedBrowse = allowUnrestricted
	}
	if err := store.UpdateRecord(record); err != nil {
		return 0, fmt.Errorf("update browse roots: persist policy: %w", err)
	}
	return len(roots), nil
}

func clearPersistedAuthSessions(dbPath string) error {
	path := filepath.Join(filepath.Dir(strings.TrimSpace(dbPath)), sessionFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("change auth password: revoke retained sessions: %w", err)
	}
	return nil
}
