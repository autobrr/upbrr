// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/autobrr/upbrr/pkg/api"
)

// SyncCookieEncryptionWithAuth ensures encrypted cookie state is synchronized with the current
// web auth helper and performs key rotation when auth details have changed.
func SyncCookieEncryptionWithAuth(ctx context.Context, db *sql.DB, dbPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	keyManager := NewKeyManager(db)
	_, err := keyManager.InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		return err
	}

	return nil
}

// EnsureCookieMigration handles the automatic migration of cookies from file-based storage
// to the encrypted database. It should be called early during app initialization.
//
// If old cookie files (.txt or .json) are found in the cookies directory, the function will:
// 1. Initialize or retrieve the encryption key from existing web auth details
// 2. Create a CookieStore instance
// 3. Migrate all cookies from files to the encrypted database
// 4. Delete the old cookie files
//
// If no old files are found, it returns immediately with no action.
func EnsureCookieMigration(ctx context.Context, db *sql.DB, dbPath string, cookiesDir string, logger api.Logger) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = api.NopLogger{}
	}

	// Check if cookies directory exists and has any .txt or .json files
	if !hasLegacyCookieFiles(cookiesDir) {
		return nil
	}

	// Initialize encryption key
	keyManager := NewKeyManager(db)
	encryptionKey, err := keyManager.InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		return err
	}

	// Create cookie store
	store, err := NewCookieStore(db)
	if err != nil {
		return err
	}

	// Perform migration
	migratedCount, err := MigrateFromFilesToDB(ctx, cookiesDir, store, encryptionKey, logger)
	if err != nil {
		return err
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

// hasLegacyCookieFiles checks if there are any .txt or .json cookie files in the directory.
func hasLegacyCookieFiles(cookiesDir string) bool {
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
