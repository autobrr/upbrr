// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
	sqlite3lib "modernc.org/sqlite/lib"

	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func LoadTrackerCookieMap(ctx context.Context, dbPath string, trackerID string) (map[string]string, error) {
	if ctx == nil {
		return nil, errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return nil, errors.New("cookies: tracker id is required")
	}

	var storedValues map[string]string

	if store, key, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		defer func() {
			_ = repo.Close()
		}()

		values, err := store.GetAllTrackerCookies(ctx, normalizedTrackerID, key)
		if err != nil {
			return nil, fmt.Errorf("cookies: load tracker %s from db: %w", normalizedTrackerID, err)
		}
		if len(values) > 0 {
			storedValues = values
		}
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		return nil, err
	}

	fileValues, err := loadTrackerCookieMapFromFiles(dbPath, normalizedTrackerID)
	if err == nil {
		return mergeCookieMaps(storedValues, fileValues), nil
	}
	if len(storedValues) > 0 {
		return storedValues, nil
	}

	return nil, err
}

func LoadTrackerHTTPCookies(ctx context.Context, dbPath string, trackerID string, domain string) ([]*http.Cookie, error) {
	if ctx == nil {
		return nil, errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return nil, errors.New("cookies: tracker id is required")
	}

	var storedValues map[string]string

	if store, key, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		defer func() {
			_ = repo.Close()
		}()

		values, err := store.GetAllTrackerCookies(ctx, normalizedTrackerID, key)
		if err != nil {
			return nil, fmt.Errorf("cookies: load tracker %s from db: %w", normalizedTrackerID, err)
		}
		if len(values) > 0 {
			storedValues = values
		}
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		return nil, err
	}

	fileCookies, err := loadTrackerHTTPCookiesFromFiles(dbPath, normalizedTrackerID, domain)
	if err == nil {
		return CookieMapToHTTPCookies(mergeCookieMaps(storedValues, httpCookiesToMap(fileCookies)), domain), nil
	}
	if len(storedValues) > 0 {
		return CookieMapToHTTPCookies(storedValues, domain), nil
	}

	return nil, err
}

func SaveTrackerCookieMap(ctx context.Context, dbPath string, trackerID string, values map[string]string) error {
	if ctx == nil {
		return errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return errors.New("cookies: tracker id is required")
	}

	store, key, repo, err := openTrackerCookieStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = repo.Close()
	}()

	if err := store.RunInTransaction(ctx, func(tx *sql.Tx) error {
		if err := store.DeleteAllTrackerCookiesTx(ctx, tx, normalizedTrackerID); err != nil {
			return fmt.Errorf("cookies: reset tracker %s in db: %w", normalizedTrackerID, err)
		}

		for name, value := range values {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			if err := store.SaveCookieTx(ctx, tx, normalizedTrackerID, trimmedName, value, key); err != nil {
				return fmt.Errorf("cookies: save tracker %s cookie %s: %w", normalizedTrackerID, trimmedName, err)
			}
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func SaveTrackerHTTPCookies(ctx context.Context, dbPath string, trackerID string, values []*http.Cookie) error {
	return SaveTrackerCookieMap(ctx, dbPath, trackerID, httpCookiesToMap(values))
}

func DeleteTrackerCookies(ctx context.Context, dbPath string, trackerID string) error {
	if ctx == nil {
		return errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return errors.New("cookies: tracker id is required")
	}

	var deleteErr error
	if store, _, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		if err := store.DeleteAllTrackerCookies(ctx, normalizedTrackerID); err != nil {
			deleteErr = fmt.Errorf("cookies: delete tracker %s from db: %w", normalizedTrackerID, err)
		}
		_ = repo.Close()
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		deleteErr = err
	}

	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, normalizedTrackerID, ".txt", ".json") {
		if removeErr := os.Remove(candidate); removeErr != nil && !os.IsNotExist(removeErr) && deleteErr == nil {
			deleteErr = fmt.Errorf("cookies: delete tracker %s legacy cookie file %s: %w", normalizedTrackerID, candidate, removeErr)
		}
	}

	return deleteErr
}

func CookieMapToHTTPCookies(values map[string]string, domain string) []*http.Cookie {
	trimmedDomain := strings.TrimSpace(domain)
	result := make([]*http.Cookie, 0, len(values))
	for name, value := range values {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" || value == "" {
			continue
		}
		//nolint:gosec // client-side cookies loaded from files do not require strict server security flags
		result = append(result, &http.Cookie{
			Name:   trimmedName,
			Value:  value,
			Domain: trimmedDomain,
			Path:   "/",
		})
	}
	return result
}

func openTrackerCookieStore(ctx context.Context, dbPath string) (*CookieStore, []byte, *servicedb.SQLiteRepository, error) {
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cookies: open db: %w", err)
	}

	store, err := NewCookieStore(repo.RawDB())
	if err != nil {
		_ = repo.Close()
		return nil, nil, nil, fmt.Errorf("cookies: create cookie store: %w", err)
	}

	key, err := NewKeyManager(repo.RawDB()).InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		_ = repo.Close()
		if errors.Is(err, ErrAuthHelperUnavailable) {
			return nil, nil, nil, ErrAuthHelperUnavailable
		}
		if isMissingCookieSchemaError(err) {
			return nil, nil, nil, ErrAuthHelperUnavailable
		}
		return nil, nil, nil, fmt.Errorf("cookies: initialize encryption key: %w", err)
	}

	return store, key, repo, nil
}

func loadTrackerCookieMapFromFiles(dbPath string, trackerID string) (map[string]string, error) {
	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, trackerID, ".txt", ".json") {
		switch strings.ToLower(filepath.Ext(candidate)) {
		case ".txt":
			cookies, err := commonhttp.LoadNetscapeCookies(candidate, "")
			if err != nil {
				continue
			}
			values := httpCookiesToMap(cookies)
			if len(values) > 0 {
				return values, nil
			}
		case ".json":
			values, err := commonhttp.LoadJSONCookieMap(candidate)
			if err != nil {
				continue
			}
			if len(values) > 0 {
				return values, nil
			}
		}
	}

	return nil, fmt.Errorf("cookies: no cookies found for tracker %s", trackerID)
}

func loadTrackerHTTPCookiesFromFiles(dbPath string, trackerID string, domain string) ([]*http.Cookie, error) {
	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, trackerID, ".txt", ".json") {
		switch strings.ToLower(filepath.Ext(candidate)) {
		case ".txt":
			cookies, err := commonhttp.LoadNetscapeCookies(candidate, domain)
			if err != nil {
				continue
			}
			if len(cookies) > 0 {
				return cookies, nil
			}
		case ".json":
			values, err := commonhttp.LoadJSONCookieMap(candidate)
			if err != nil {
				continue
			}
			cookies := CookieMapToHTTPCookies(values, domain)
			if len(cookies) > 0 {
				return cookies, nil
			}
		}
	}

	return nil, fmt.Errorf("cookies: no cookies found for tracker %s", trackerID)
}

func httpCookiesToMap(values []*http.Cookie) map[string]string {
	result := make(map[string]string)
	for _, value := range values {
		if value == nil {
			continue
		}
		name := strings.TrimSpace(value.Name)
		cookieValue := strings.TrimSpace(value.Value)
		if name == "" || cookieValue == "" {
			continue
		}
		result[name] = cookieValue
	}
	return result
}

func mergeCookieMaps(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 {
		return override
	}
	if len(override) == 0 {
		return base
	}

	merged := make(map[string]string, len(base)+len(override))
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range override {
		merged[name] = value
	}

	return merged
}

func isMissingCookieSchemaError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3lib.SQLITE_ERROR {
		return false
	}

	return strings.Contains(strings.ToLower(sqliteErr.Error()), "no such table")
}
