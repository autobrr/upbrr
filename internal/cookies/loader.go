// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// LoadTrackerCookieMap returns cookies for trackerID from encrypted database
// storage and legacy cookie files. Database values override legacy file values
// when both sources contain the same cookie name.
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
		return mergeCookieMaps(fileValues, storedValues), nil
	}
	if len(storedValues) > 0 {
		return storedValues, nil
	}

	return nil, err
}

// LoadTrackerHTTPCookies returns tracker cookies as HTTP cookies for domain,
// using the same database-plus-legacy-file merge as LoadTrackerCookieMap.
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
		return CookieMapToHTTPCookies(mergeCookieMaps(httpCookiesToMap(fileCookies), storedValues), domain), nil
	}
	if len(storedValues) > 0 {
		return CookieMapToHTTPCookies(storedValues, domain), nil
	}

	return nil, err
}

// SaveTrackerCookieMap replaces trackerID's encrypted database cookies with
// non-empty entries from values. It requires usable web auth material and does
// not remove legacy cookie files; later loads let database values override
// same-named legacy file values.
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

// SaveTrackerHTTPCookies stores HTTP cookies by cookie name for trackerID.
func SaveTrackerHTTPCookies(ctx context.Context, dbPath string, trackerID string, values []*http.Cookie) error {
	return SaveTrackerCookieMap(ctx, dbPath, trackerID, httpCookiesToMap(values))
}

// DeleteTrackerCookies removes trackerID cookies from encrypted database
// storage and deletes matching legacy cookie files when present. Database
// delete failures stop before legacy files are touched; legacy delete failures
// restore earlier removed legacy candidates and prior database cookies before
// returning.
func DeleteTrackerCookies(ctx context.Context, dbPath string, trackerID string) error {
	if ctx == nil {
		return errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return errors.New("cookies: tracker id is required")
	}

	var (
		dbStore      *CookieStore
		dbKey        []byte
		storedValues map[string]string
		dbDeleted    bool
	)
	if store, key, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		defer func() {
			_ = repo.Close()
		}()
		dbStore = store
		dbKey = key
		var err error
		storedValues, err = store.GetAllTrackerCookies(ctx, normalizedTrackerID, key)
		if err != nil {
			return fmt.Errorf("cookies: snapshot tracker %s from db: %w", normalizedTrackerID, err)
		}
		if err := store.DeleteAllTrackerCookies(ctx, normalizedTrackerID); err != nil {
			return fmt.Errorf("cookies: delete tracker %s from db: %w", normalizedTrackerID, err)
		}
		dbDeleted = true
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		return err
	}

	var removedLegacy []legacyCookieCandidateSnapshot
	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, normalizedTrackerID, ".txt", ".json") {
		snapshot, removed, removeErr := removeLegacyCookieCandidate(candidate)
		if removeErr != nil {
			deleteErr := fmt.Errorf("cookies: delete tracker %s legacy cookie file %s: %w", normalizedTrackerID, candidate, removeErr)
			legacyRestoreErr := restoreLegacyCookieCandidates(removedLegacy)
			if dbDeleted && len(storedValues) > 0 {
				if restoreErr := restoreTrackerCookies(contextWithoutCancel(ctx), dbStore, dbKey, normalizedTrackerID, storedValues); restoreErr != nil {
					return errors.Join(deleteErr, legacyRestoreErr, restoreErr)
				}
			}
			return errors.Join(deleteErr, legacyRestoreErr)
		}
		if removed {
			removedLegacy = append(removedLegacy, snapshot)
		}
	}

	return nil
}

// legacyCookieCandidateKind records the filesystem object type needed to
// recreate a removed legacy cookie candidate during rollback.
type legacyCookieCandidateKind int

const (
	legacyCookieCandidateFile legacyCookieCandidateKind = iota + 1
	legacyCookieCandidateDir
	legacyCookieCandidateSymlink
)

// legacyCookieCandidateSnapshot captures enough state to restore a removed
// legacy cookie file, empty directory, or symlink if a later delete fails.
type legacyCookieCandidateSnapshot struct {
	path       string
	mode       fs.FileMode
	kind       legacyCookieCandidateKind
	data       []byte
	linkTarget string
}

// removeLegacyCookieCandidate snapshots and removes a single legacy cookie
// candidate. Missing paths are ignored; unsupported types and non-empty
// directories return errors without reporting the candidate as removed.
func removeLegacyCookieCandidate(path string) (legacyCookieCandidateSnapshot, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return legacyCookieCandidateSnapshot{}, false, nil
	}
	if err != nil {
		return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("stat legacy cookie candidate %s: %w", path, err)
	}

	snapshot := legacyCookieCandidateSnapshot{path: path, mode: info.Mode()}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("read legacy cookie symlink %s: %w", path, err)
		}
		snapshot.kind = legacyCookieCandidateSymlink
		snapshot.linkTarget = target
	case info.IsDir():
		entries, err := os.ReadDir(path)
		if err != nil {
			return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("read legacy cookie dir %s: %w", path, err)
		}
		if len(entries) > 0 {
			if err := os.Remove(path); err != nil {
				return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("remove non-empty legacy cookie dir %s: %w", path, err)
			}
			return legacyCookieCandidateSnapshot{}, false, nil
		}
		snapshot.kind = legacyCookieCandidateDir
	case info.Mode().IsRegular():
		data, err := os.ReadFile(path)
		if err != nil {
			return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("read legacy cookie file %s: %w", path, err)
		}
		snapshot.kind = legacyCookieCandidateFile
		snapshot.data = data
	default:
		return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("unsupported legacy cookie candidate type %s", info.Mode().Type())
	}

	if err := os.Remove(path); err != nil {
		return legacyCookieCandidateSnapshot{}, false, fmt.Errorf("remove legacy cookie candidate %s: %w", path, err)
	}
	return snapshot, true, nil
}

// restoreLegacyCookieCandidates restores removed candidates in reverse order so
// rollback rebuilds parent paths after later candidates have been handled.
func restoreLegacyCookieCandidates(snapshots []legacyCookieCandidateSnapshot) error {
	var errs []error
	for i := len(snapshots) - 1; i >= 0; i-- {
		if err := snapshots[i].restore(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// restore recreates the recorded legacy cookie candidate at its original path.
func (s legacyCookieCandidateSnapshot) restore() error {
	switch s.kind {
	case legacyCookieCandidateFile:
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return fmt.Errorf("cookies: restore legacy cookie file dir %s: %w", s.path, err)
		}
		if err := os.WriteFile(s.path, s.data, s.mode.Perm()); err != nil {
			return fmt.Errorf("cookies: restore legacy cookie file %s: %w", s.path, err)
		}
	case legacyCookieCandidateDir:
		if err := os.MkdirAll(s.path, s.mode.Perm()); err != nil {
			return fmt.Errorf("cookies: restore legacy cookie dir %s: %w", s.path, err)
		}
	case legacyCookieCandidateSymlink:
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return fmt.Errorf("cookies: restore legacy cookie symlink dir %s: %w", s.path, err)
		}
		if err := os.Symlink(s.linkTarget, s.path); err != nil {
			return fmt.Errorf("cookies: restore legacy cookie symlink %s: %w", s.path, err)
		}
	default:
		return fmt.Errorf("cookies: restore legacy cookie candidate %s: unknown snapshot kind", s.path)
	}
	return nil
}

// restoreTrackerCookies replaces trackerID's database cookies with values after
// a partial legacy-file cleanup failure.
func restoreTrackerCookies(ctx context.Context, store *CookieStore, key []byte, trackerID string, values map[string]string) error {
	if store == nil {
		return errors.New("cookies: restore tracker cookies: store is required")
	}
	return store.RunInTransaction(ctx, func(tx *sql.Tx) error {
		if err := store.DeleteAllTrackerCookiesTx(ctx, tx, trackerID); err != nil {
			return err
		}
		for name, value := range values {
			if err := store.SaveCookieTx(ctx, tx, trackerID, name, value, key); err != nil {
				return fmt.Errorf("restore cookie %s: %w", name, err)
			}
		}
		return nil
	})
}

// contextWithoutCancel preserves context values for rollback work while
// detaching cancellation and deadline state.
func contextWithoutCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

// CookieMapToHTTPCookies converts entries with non-blank names and non-empty
// values to HTTP cookies scoped to domain and path "/". Names and domain are
// trimmed; values are copied unchanged.
func CookieMapToHTTPCookies(values map[string]string, domain string) []*http.Cookie {
	trimmedDomain := strings.TrimSpace(domain)
	result := make([]*http.Cookie, 0, len(values))
	for name, value := range values {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" || value == "" {
			continue
		}
		// #nosec G124 -- Outbound tracker cookie jar entries preserve user-imported cookie attributes.
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
	maps.Copy(merged, base)
	maps.Copy(merged, override)

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
