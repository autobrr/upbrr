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
		ctx = context.Background()
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return nil, errors.New("cookies: tracker id is required")
	}

	if store, key, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		defer func() {
			_ = repo.Close()
		}()

		values, err := store.GetAllTrackerCookies(ctx, normalizedTrackerID, key)
		if err != nil {
			return nil, fmt.Errorf("cookies: load tracker %s from db: %w", normalizedTrackerID, err)
		}
		if len(values) > 0 {
			return values, nil
		}
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		return nil, err
	}

	return loadTrackerCookieMapFromFiles(dbPath, normalizedTrackerID)
}

func LoadTrackerHTTPCookies(ctx context.Context, dbPath string, trackerID string, domain string) ([]*http.Cookie, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return nil, errors.New("cookies: tracker id is required")
	}

	if store, key, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		defer func() {
			_ = repo.Close()
		}()

		values, err := store.GetAllTrackerCookies(ctx, normalizedTrackerID, key)
		if err != nil {
			return nil, fmt.Errorf("cookies: load tracker %s from db: %w", normalizedTrackerID, err)
		}
		if len(values) > 0 {
			return CookieMapToHTTPCookies(values, domain), nil
		}
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		return nil, err
	}

	return loadTrackerHTTPCookiesFromFiles(dbPath, normalizedTrackerID, domain)
}

func SaveTrackerCookieMap(ctx context.Context, dbPath string, trackerID string, values map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
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
		ctx = context.Background()
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
	repo, err := servicedb.OpenWithLogger(dbPath, api.NopLogger{})
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
