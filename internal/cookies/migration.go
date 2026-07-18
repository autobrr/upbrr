// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
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
type FailedCookie struct {
	TrackerID  string
	CookieName string
}

// MigrateFromFilesToDB imports top-level .txt and .json cookie files into the
// encrypted database. It returns the stored-cookie count and storage failures;
// file parse failures are logged and omitted from both results.
func MigrateFromFilesToDB(ctx context.Context, cookiesDir string, store *CookieStore, key []byte, logger api.Logger) (int, []FailedCookie, error) {
	if ctx == nil {
		return 0, nil, errors.New("cookies: context is required")
	}
	if logger == nil {
		logger = api.NopLogger{}
	}

	// Check if cookies directory exists
	info, err := os.Stat(cookiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil // No cookies directory, nothing to migrate
		}
		return 0, nil, fmt.Errorf("failed to stat cookies directory: %w", err)
	}

	if !info.IsDir() {
		return 0, nil, fmt.Errorf("cookie path is not a directory: %s", cookiesDir)
	}

	// List files in the cookies directory
	entries, err := os.ReadDir(cookiesDir)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read cookies directory: %w", err)
	}

	migratedCount := 0
	failedCookies := make([]FailedCookie, 0)

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
			logger.Warnf("cookies: failed to parse cookie file tracker=%s file=%s: %v", trackerID, filename, parseErr)
			continue
		}

		if len(cookies) == 0 {
			continue
		}

		// Store each cookie in the database
		for cookieName, cookieValue := range cookies {
			if err := store.SaveCookie(ctx, trackerID, cookieName, cookieValue, key); err != nil {
				failedCookies = append(failedCookies, FailedCookie{TrackerID: trackerID, CookieName: cookieName})
				redactedValue := redaction.RedactValue(cookieValue, nil)
				logger.Warnf("cookies: failed to migrate cookie tracker=%s cookie=%s value=%v: %v", trackerID, cookieName, redactedValue, err)
				continue
			}

			migratedCount++
		}
	}

	return migratedCount, failedCookies, nil
}

// DeleteMigratedCookieFiles removes every top-level .txt and .json file from
// cookiesDir. It does not verify that an individual file was migrated.
func DeleteMigratedCookieFiles(cookiesDir string, logger api.Logger) error {
	return deleteMigratedCookieFiles(cookiesDir, logger)
}

func deleteMigratedCookieFiles(cookiesDir string, logger api.Logger) error {
	if logger == nil {
		logger = api.NopLogger{}
	}

	// Check if cookies directory exists
	info, err := os.Stat(cookiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist, nothing to delete
		}
		return fmt.Errorf("failed to stat cookies directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("cookie path is not a directory: %s", cookiesDir)
	}

	// List files in the cookies directory
	entries, err := os.ReadDir(cookiesDir)
	if err != nil {
		return fmt.Errorf("failed to read cookies directory: %w", err)
	}

	deleteErrs := make([]error, 0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		ext := strings.ToLower(filepath.Ext(filename))

		// Only delete .txt and .json files
		if ext != ".txt" && ext != ".json" {
			continue
		}

		filePath := filepath.Join(cookiesDir, filename)
		if err := os.Remove(filePath); err != nil {
			logger.Warnf("cookies: failed to delete migrated cookie file path=%s: %v", filePath, err)
			deleteErrs = append(deleteErrs, fmt.Errorf("remove %s: %w", filePath, err))
			continue
		}
	}

	if len(deleteErrs) > 0 {
		return fmt.Errorf("failed to delete one or more migrated cookie files: %w", errors.Join(deleteErrs...))
	}

	return nil
}
