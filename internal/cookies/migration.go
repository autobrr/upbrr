// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	files    []migratedCookieFile
}

type migratedCookieFile struct {
	name string
	info fs.FileInfo
}

func legacyCookieFilename(name string) (stem, ext string) {
	extension := filepath.Ext(name)
	ext = strings.ToLower(extension)
	if ext != ".txt" && ext != ".json" {
		return "", ""
	}
	return strings.TrimSuffix(name, extension), ext
}

// compareLegacyCookieNames orders Netscape before JSON, then exact names
// before aliases. It also makes the order of multiple aliases deterministic.
func compareLegacyCookieNames(a, b string) int {
	aStem, aExt := legacyCookieFilename(a)
	bStem, bExt := legacyCookieFilename(b)
	if byExt := cmp.Compare(bExt, aExt); byExt != 0 {
		return byExt
	}
	aExact := a == strings.ToUpper(aStem)+aExt
	bExact := b == strings.ToUpper(bStem)+bExt
	if aExact != bExact {
		if aExact {
			return -1
		}
		return 1
	}
	return cmp.Compare(a, b)
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

	// Import less preferred aliases first so the same conflicting cookie wins
	// as with direct legacy loading. All successfully imported sources can be
	// cleaned, so a losing alias cannot overwrite the winner on the next start.
	slices.SortStableFunc(entries, func(a, b fs.DirEntry) int {
		return compareLegacyCookieNames(b.Name(), a.Name())
	})

	var result MigrationResult

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		stem, ext := legacyCookieFilename(filename)
		if stem == "" {
			continue
		}
		trackerID := strings.ToUpper(stem)
		filePath := filepath.Join(cookiesDir, filename)
		info, err := legacyCookieFileInfo(filePath)
		if err != nil {
			result.Failures = append(result.Failures, FailedCookie{TrackerID: trackerID})
			logger.Warnf("cookies: failed to inspect cookie file tracker=%s file=%s: %v", trackerID, filename, err)
			continue
		}

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
			result.files = append(result.files, migratedCookieFile{name: filename, info: info})
		}
	}
	return result, nil
}

// legacyCookieFileInfo binds the identity to the open file, not a later path
// lookup (Windows resolves the identity of os.Stat results lazily).
func legacyCookieFileInfo(path string) (fs.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cookie export: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat cookie export: %w", err)
	}
	return info, nil
}

// Cleanup removes successfully migrated files that still match their source
// snapshot. Failed migrations and changed exports remain available for retry.
func (r MigrationResult) Cleanup(cookiesDir string, logger api.Logger) error {
	if len(r.Failures) > 0 {
		return nil
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	var deleteErrs []error
	for _, source := range r.files {
		filePath := filepath.Join(cookiesDir, source.name)
		info, err := legacyCookieFileInfo(filePath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil {
			if !os.SameFile(source.info, info) || source.info.Size() != info.Size() || !source.info.ModTime().Equal(info.ModTime()) {
				continue
			}
			err = os.Remove(filePath)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.Warnf("cookies: failed to delete migrated cookie file path=%s: %v", filePath, err)
			deleteErrs = append(deleteErrs, fmt.Errorf("remove %s: %w", filePath, err))
		}
	}
	if len(deleteErrs) > 0 {
		return fmt.Errorf("failed to delete one or more migrated cookie files: %w", errors.Join(deleteErrs...))
	}
	return nil
}
