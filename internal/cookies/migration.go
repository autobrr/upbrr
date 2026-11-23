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
	"strings"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

// FailedCookie identifies a tracker/cookie pair that could not be migrated.
// CookieName is empty when the source file could not be parsed.
type FailedCookie struct {
	TrackerID  string
	CookieName string
}

// MigrationResult records the cookies and exact files successfully migrated.
type MigrationResult struct {
	Count    int
	Failures []FailedCookie
	files    []string
}

// MigrateFromFilesToDB imports top-level .txt and .json cookie files into the
// encrypted database. Use the result to clean up only after migration succeeds.
func MigrateFromFilesToDB(ctx context.Context, cookiesDir string, store *CookieStore, key []byte, logger api.Logger) (MigrationResult, error) {
	if store == nil {
		return MigrationResult{}, errors.New("cookies: cookie store is required")
	}
	var result MigrationResult
	err := store.RunInTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = MigrateFromFilesToDBTx(ctx, cookiesDir, store, tx, key, logger)
		return err
	})
	return result, err
}

// MigrateFromFilesToDBTx imports legacy cookie files through tx so callers can
// commit or roll back the cookie writes with a surrounding database operation.
func MigrateFromFilesToDBTx(
	ctx context.Context,
	cookiesDir string,
	store *CookieStore,
	tx *sql.Tx,
	key []byte,
	logger api.Logger,
) (MigrationResult, error) {
	if store == nil {
		return MigrationResult{}, errors.New("cookies: cookie store is required")
	}
	if tx == nil {
		return MigrationResult{}, errors.New("cookies: transaction is required")
	}
	if ctx == nil {
		return MigrationResult{}, errors.New("cookies: context is required")
	}
	// Take the SQLite write lock before reading files. DeleteTrackerCookies
	// holds it until the database rows and their source files are removed.
	if err := lockCookieWrites(ctx, tx); err != nil && !isMissingCookieSchemaError(err) {
		return MigrationResult{}, fmt.Errorf("cookies: lock migration: %w", err)
	}
	return migrateFromFilesToDB(ctx, cookiesDir, key, logger, func(
		ctx context.Context,
		trackerID, cookieName, cookieValue string,
		key []byte,
	) error {
		return store.saveCookie(ctx, tx, trackerID, cookieName, cookieValue, key, true)
	})
}

type saveCookieFunc func(context.Context, string, string, string, []byte) error

func migrateFromFilesToDB(
	ctx context.Context,
	cookiesDir string,
	key []byte,
	logger api.Logger,
	saveCookie saveCookieFunc,
) (MigrationResult, error) {
	if ctx == nil {
		return MigrationResult{}, errors.New("cookies: context is required")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}

	// Check if cookies directory exists
	info, err := os.Stat(cookiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return MigrationResult{}, nil // No cookies directory, nothing to migrate
		}
		return MigrationResult{}, fmt.Errorf("failed to stat cookies directory: %w", err)
	}

	if !info.IsDir() {
		return MigrationResult{}, fmt.Errorf("cookie path is not a directory: %s", cookiesDir)
	}

	// List files in the cookies directory
	entries, err := os.ReadDir(cookiesDir)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("failed to read cookies directory: %w", err)
	}

	var result MigrationResult

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		ext := strings.ToLower(filepath.Ext(filename))

		// Only process .txt and .json files
		if ext != ".txt" && ext != ".json" {
			continue
		}

		// Extract tracker name from filename (e.g., "ar.txt" -> "ar")
		trackerID := strings.TrimSuffix(filename, ext)
		if trackerID == "" {
			continue
		}

		filePath := filepath.Join(cookiesDir, filename)

		var cookies map[string]string
		var parseErr error

		switch ext {
		case ".txt":
			// Parse Netscape format cookies
			// No expected domain is supplied; parse all valid entries from the file.
			parsedCookies, err := commonhttp.LoadNetscapeCookies(filePath, "")
			if err != nil {
				parseErr = fmt.Errorf("failed to parse Netscape cookies from %s: %w", filename, err)
			} else {
				cookies = make(map[string]string)
				for _, c := range parsedCookies {
					cookies[c.Name] = c.Value
				}
			}

		case ".json":
			// Parse JSON format cookies
			parsedCookies, err := commonhttp.LoadJSONCookieMap(filePath)
			if err != nil {
				parseErr = fmt.Errorf("failed to parse JSON cookies from %s: %w", filename, err)
			} else {
				cookies = parsedCookies
			}
		}

		if parseErr != nil {
			result.Failures = append(result.Failures, FailedCookie{TrackerID: trackerID})
			logger.Warnf("cookies: failed to parse cookie file tracker=%s file=%s: %v", trackerID, filename, parseErr)
			continue
		}

		if len(cookies) == 0 {
			continue
		}

		before := result.Count
		retained := false
		for cookieName, cookieValue := range cookies {
			if err := saveCookie(ctx, trackerID, cookieName, cookieValue, key); err != nil {
				if errors.Is(err, errLegacyCookieRetained) {
					retained = true
					logger.Debugf("cookies: retained legacy file tracker=%s reason=managed_session", trackerID)
					break
				}
				result.Failures = append(result.Failures, FailedCookie{TrackerID: trackerID, CookieName: cookieName})
				redactedValue := redaction.RedactValue(cookieValue, nil)
				logger.Warnf("cookies: failed to migrate cookie tracker=%s cookie=%s value=%v: %v", trackerID, cookieName, redactedValue, err)
				continue
			}

			result.Count++
		}
		if !retained && result.Count > before {
			result.files = append(result.files, filename)
		}
	}
	return result, nil
}

// Cleanup removes only successfully migrated files. A genuine parse or storage
// failure preserves the original files for retry.
func (r MigrationResult) Cleanup(cookiesDir string, logger api.Logger) error {
	if len(r.Failures) > 0 {
		return nil
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	var deleteErrs []error
	for _, filename := range r.files {
		filePath := filepath.Join(cookiesDir, filename)
		if err := os.Remove(filePath); err != nil {
			logger.Warnf("cookies: failed to delete migrated cookie file path=%s: %v", filePath, err)
			deleteErrs = append(deleteErrs, fmt.Errorf("remove %s: %w", filePath, err))
		}
	}
	if len(deleteErrs) > 0 {
		return fmt.Errorf("failed to delete one or more migrated cookie files: %w", errors.Join(deleteErrs...))
	}
	return nil
}
