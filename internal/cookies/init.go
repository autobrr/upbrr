// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/autobrr/upbrr/pkg/api"
)

// SyncCookieEncryptionWithAuth synchronizes encrypted cookie state with the
// current web auth helper and rotates encrypted rows when auth details change.
// It returns ErrAuthHelperUnavailable when no usable auth material exists.
func SyncCookieEncryptionWithAuth(ctx context.Context, db *sql.DB, dbPath string) error {
	if ctx == nil {
		return errors.New("cookies: context is required")
	}

	keyManager := NewKeyManager(db)
	_, err := keyManager.InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		return err
	}

	return nil
}

// SyncCookieEncryptionWithAuthTx synchronizes encrypted cookie state inside an
// existing transaction so callers can commit or roll back cookie metadata with
// the surrounding database write.
func SyncCookieEncryptionWithAuthTx(ctx context.Context, tx *sql.Tx, dbPath string) error {
	_, err := InitializeEncryptionKeyTx(ctx, tx, dbPath)
	return err
}

// InitializeEncryptionKeyTx derives the current cookie key and synchronizes
// encrypted cookie state inside tx. The caller owns commit or rollback.
func InitializeEncryptionKeyTx(ctx context.Context, tx *sql.Tx, dbPath string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("cookies: context is required")
	}
	if tx == nil {
		return nil, errors.New("cookies: transaction is required")
	}

	key, err := initializeEncryptionKey(ctx, tx, dbPath, func(ctx context.Context, oldKey, newKey []byte) error {
		return reencryptCookiesTx(ctx, tx, oldKey, newKey)
	})
	if err != nil {
		return nil, err
	}

	return key, nil
}

// EnsureCookieMigration imports top-level .txt and .json cookie files into the
// encrypted database. Missing or unreadable legacy directories are treated as
// having no files. Storage failures retain every legacy file but are logged
// rather than returned. When at least one cookie is stored and no storage
// failure occurs, all top-level .txt and .json files are removed; parse and
// cleanup failures are logged rather than returned.
func EnsureCookieMigration(ctx context.Context, db *sql.DB, dbPath string, cookiesDir string, logger api.Logger) error {
	if ctx == nil {
		return errors.New("cookies: context is required")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}

	// Check if cookies directory exists and has any .txt or .json files
	if !HasLegacyCookieFiles(cookiesDir) {
		return nil
	}

	// Initialize encryption key
	keyManager := NewKeyManager(db)
	encryptionKey, err := keyManager.InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize encryption key: %w", err)
	}

	// Create cookie store
	store, err := NewCookieStore(db)
	if err != nil {
		return fmt.Errorf("failed to create cookie store: %w", err)
	}

	// Perform migration
	migratedCount, failedCookies, err := MigrateFromFilesToDB(ctx, cookiesDir, store, encryptionKey, logger)
	if err != nil {
		return fmt.Errorf("failed to migrate cookies from files to DB: %w", err)
	}
	if len(failedCookies) > 0 {
		return nil
	}

	// Only delete old files if migration was successful
	if migratedCount > 0 {
		if err := deleteMigratedCookieFiles(cookiesDir, logger); err != nil {
			logger.Warnf("cookies: migration cleanup failed dir=%s migrated=%d: %v", cookiesDir, migratedCount, err)
			// Don't return error here - migration was successful, cleanup is secondary
			return nil
		}
	}

	return nil
}

// HasLegacyCookieFiles reports whether cookiesDir contains top-level .txt or
// .json files eligible for legacy migration.
func HasLegacyCookieFiles(cookiesDir string) bool {
	info, err := os.Stat(cookiesDir)
	if err != nil {
		return false // Directory doesn't exist or can't be read
	}

	if !info.IsDir() {
		return false // Not a directory
	}

	entries, err := os.ReadDir(cookiesDir)
	if err != nil {
		return false // Can't read directory
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		if ext == ".txt" || ext == ".json" {
			return true
		}
	}

	return false
}
