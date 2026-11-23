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
	"slices"
	"strings"

	"modernc.org/sqlite"
	sqlite3lib "modernc.org/sqlite/lib"

	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

// ErrTrackerCookiesNotFound reports that neither encrypted storage nor legacy
// cookie files contained usable cookies for a tracker.
var ErrTrackerCookiesNotFound = errors.New("cookies: tracker cookies not found")

// ErrTrackerSessionChanged reports that the validated session was replaced.
var ErrTrackerSessionChanged = errors.New("cookies: tracker session changed")

// A name that cannot be a wire cookie. Save it in the same transaction as the
// encrypted cookies so a credential change cannot expose a stale session.
const (
	trackerCookieOwnerKey = "upbrr:cookie-owner"
	importedCookieOwner   = "imported"
)

func managedTrackerCookies(values map[string]string, expectedOwner []string) (map[string]string, bool, error) {
	owner, managed := values[trackerCookieOwnerKey]
	if !managed {
		return values, false, nil
	}
	delete(values, trackerCookieOwnerKey)
	if len(values) == 0 || (owner != importedCookieOwner && (len(expectedOwner) != 1 || owner != expectedOwner[0])) {
		return nil, true, ErrTrackerCookiesNotFound
	}
	return values, true, nil
}

// LoadTrackerCookieMap returns cookies for trackerID from encrypted database
// storage and legacy cookie files. Database values override legacy file values
// when both sources contain the same cookie name.
func LoadTrackerCookieMap(ctx context.Context, dbPath string, trackerID string, expectedOwner ...string) (map[string]string, error) {
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
	} else if err := allowLegacyCookieFallback(ctx, dbPath, normalizedTrackerID, expectedOwner); err != nil {
		return nil, err
	}

	if values, managed, err := managedTrackerCookies(storedValues, expectedOwner); managed {
		return values, err
	}
	fileValues, err := loadTrackerCookieMapFromFiles(dbPath, normalizedTrackerID)
	if err == nil {
		delete(fileValues, trackerCookieOwnerKey)
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
	session, err := LoadTrackerHTTPSession(ctx, dbPath, trackerID, domain, "")
	return session.Cookies, err
}

// TrackerHTTPSession captures the cookies and sources observed before validation.
type TrackerHTTPSession struct {
	Cookies                          []*http.Cookie
	AccountBound                     bool
	dbPath, trackerID, domain, owner string
	storedValues, fileValues         map[string]string
}

// LoadTrackerHTTPSession accepts a managed session only for its owner. A
// missing session still returns the snapshot needed to save a fresh login.
func LoadTrackerHTTPSession(ctx context.Context, dbPath, trackerID, domain, expectedOwner string) (TrackerHTTPSession, error) {
	if ctx == nil {
		return TrackerHTTPSession{}, errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return TrackerHTTPSession{}, errors.New("cookies: tracker id is required")
	}

	session := TrackerHTTPSession{
		dbPath:    dbPath,
		trackerID: normalizedTrackerID,
		domain:    domain,
		owner:     expectedOwner,
	}

	if store, key, repo, err := openTrackerCookieStore(ctx, dbPath); err == nil {
		defer func() {
			_ = repo.Close()
		}()

		values, err := store.GetAllTrackerCookies(ctx, normalizedTrackerID, key)
		if err != nil {
			return session, fmt.Errorf("cookies: load tracker %s from db: %w", normalizedTrackerID, err)
		}
		session.storedValues = values
	} else if !errors.Is(err, ErrAuthHelperUnavailable) {
		return session, err
	} else if err := allowLegacyCookieFallback(ctx, dbPath, normalizedTrackerID, []string{expectedOwner}); err != nil {
		return session, err
	}

	if owner, managed := session.storedValues[trackerCookieOwnerKey]; managed {
		values, _, err := managedTrackerCookies(maps.Clone(session.storedValues), []string{expectedOwner})
		session.Cookies = CookieMapToHTTPCookies(values, domain)
		session.AccountBound = owner != importedCookieOwner
		return session, err
	}
	fileValues, err := loadLegacyCookieValues(ctx, dbPath, normalizedTrackerID, domain)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return session, fmt.Errorf("cookies: legacy files: %w", ctxErr)
	}
	session.fileValues = fileValues
	if err == nil || len(session.storedValues) > 0 {
		session.Cookies = CookieMapToHTTPCookies(mergeCookieMaps(fileValues, session.storedValues), domain)
		return session, nil
	}
	return session, err
}

func loadLegacyCookieValues(ctx context.Context, dbPath, trackerID, domain string) (map[string]string, error) {
	values, err := loadTrackerHTTPCookiesFromFiles(ctx, dbPath, trackerID, domain)
	if err != nil {
		return nil, err
	}
	fileValues := httpCookiesToMap(values)
	delete(fileValues, trackerCookieOwnerKey)
	return fileValues, nil
}

// Replace saves the session only while the observed cookies remain current.
// Set accountBound for credentials-based sessions and confirmed sign-outs.
func (session *TrackerHTTPSession) Replace(ctx context.Context, values []*http.Cookie, accountBound bool) error {
	owner := importedCookieOwner
	if accountBound {
		owner = session.owner
		if owner == "" {
			return errors.New("cookies: managed sessions require an owner")
		}
	}
	return saveTrackerCookieMap(ctx, session.dbPath, session.trackerID, httpCookiesToMap(values), owner, session)
}

// CheckCurrent reports if the observed session changed, without replacing it.
func (session *TrackerHTTPSession) CheckCurrent(ctx context.Context) error {
	current, err := LoadTrackerHTTPSession(ctx, session.dbPath, session.trackerID, session.domain, session.owner)
	if err != nil && !errors.Is(err, ErrTrackerCookiesNotFound) {
		return err
	}
	if !maps.Equal(current.storedValues, session.storedValues) || !maps.Equal(current.fileValues, session.fileValues) {
		return ErrTrackerSessionChanged
	}
	return nil
}

func (session *TrackerHTTPSession) verifyCurrent(ctx context.Context, tx *sql.Tx, key []byte) error {
	if err := lockCookieWrites(ctx, tx); err != nil {
		return err
	}
	stored, err := getAllTrackerCookies(ctx, tx, session.trackerID, key)
	if err != nil {
		return err
	}
	if !maps.Equal(stored, session.storedValues) {
		return ErrTrackerSessionChanged
	}
	if _, managed := stored[trackerCookieOwnerKey]; !managed {
		files, _ := loadLegacyCookieValues(ctx, session.dbPath, session.trackerID, session.domain)
		if !maps.Equal(files, session.fileValues) {
			return ErrTrackerSessionChanged
		}
	}
	return nil
}

// SaveTrackerCookieMap replaces trackerID's encrypted database cookies with
// non-empty entries from values. It requires usable web auth material and does
// not remove legacy cookie files; later loads let database values override
// same-named legacy file values.
func SaveTrackerCookieMap(ctx context.Context, dbPath string, trackerID string, values map[string]string) error {
	return saveTrackerCookieMap(ctx, dbPath, trackerID, values, "", nil)
}

// SaveImportedTrackerCookieMap makes an explicit import authoritative over
// legacy files without binding it to an account in the settings.
func SaveImportedTrackerCookieMap(ctx context.Context, dbPath, trackerID string, values map[string]string) error {
	return saveTrackerCookieMap(ctx, dbPath, trackerID, values, importedCookieOwner, nil)
}

// SaveOwnedTrackerHTTPCookies atomically replaces cookies and their owner.
// An empty cookie slice clears the session and suppresses any existing legacy fallback.
func SaveOwnedTrackerHTTPCookies(ctx context.Context, dbPath, trackerID, owner string, values []*http.Cookie) error {
	if owner == "" {
		return errors.New("cookies: managed sessions require an owner")
	}
	return saveTrackerCookieMap(ctx, dbPath, trackerID, httpCookiesToMap(values), owner, nil)
}

func saveTrackerCookieMap(ctx context.Context, dbPath, trackerID string, values map[string]string, owner string, previous *TrackerHTTPSession) error {
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

	savedValues := make(map[string]string, len(values)+1)
	if err := store.RunInTransaction(ctx, func(tx *sql.Tx) error {
		if previous != nil {
			if err := previous.verifyCurrent(ctx, tx, key); err != nil {
				return err
			}
		}
		if err := store.DeleteAllTrackerCookiesTx(ctx, tx, normalizedTrackerID); err != nil {
			return fmt.Errorf("cookies: reset tracker %s in db: %w", normalizedTrackerID, err)
		}

		// A signed-out DB-only session can be replaced by a later folder import.
		// Keep a marker when an existing export could otherwise be reinstalled.
		if owner != "" && (len(values) > 0 || hasLegacyCookieCandidate(dbPath, normalizedTrackerID)) {
			if err := store.SaveCookieTx(ctx, tx, normalizedTrackerID, trackerCookieOwnerKey, owner, key); err != nil {
				return fmt.Errorf("cookies: save managed session for %s: %w", normalizedTrackerID, err)
			}
			savedValues[trackerCookieOwnerKey] = owner
		}
		for name, value := range values {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" || trimmedName == trackerCookieOwnerKey {
				continue
			}
			if err := store.SaveCookieTx(ctx, tx, normalizedTrackerID, trimmedName, value, key); err != nil {
				return fmt.Errorf("cookies: save tracker %s cookie %s: %w", normalizedTrackerID, trimmedName, err)
			}
			savedValues[trimmedName] = value
		}

		return nil
	}); err != nil {
		return err
	}
	if previous != nil {
		previous.storedValues = savedValues
		previous.fileValues = nil
	}

	return nil
}

// SaveTrackerHTTPCookies stores HTTP cookies by cookie name for trackerID.
func SaveTrackerHTTPCookies(ctx context.Context, dbPath string, trackerID string, values []*http.Cookie) error {
	return SaveTrackerCookieMap(ctx, dbPath, trackerID, httpCookiesToMap(values))
}

func hasLegacyCookieCandidate(dbPath, trackerID string) bool {
	candidates, err := legacyTrackerCookiePaths(dbPath, trackerID)
	return err != nil || len(candidates) > 0
}

// legacyTrackerCookiePaths shares migration's case-insensitive filename rule.
// Prefer Netscape exports and exact names, as before, when several exist.
func legacyTrackerCookiePaths(dbPath, trackerID string) ([]string, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, nil
	}
	canonical, err := servicedb.CookiePath(dbPath, trackerID+".txt")
	if err != nil {
		return nil, fmt.Errorf("cookies: locate legacy cookie directory: %w", err)
	}
	dir := filepath.Dir(canonical)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cookies: list legacy cookie files: %w", err)
	}
	var names []string
	for _, entry := range entries {
		stem, _ := legacyCookieFilename(entry.Name())
		if strings.EqualFold(stem, trackerID) {
			names = append(names, entry.Name())
		}
	}
	slices.SortFunc(names, compareLegacyCookieNames)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

// DeleteTrackerCookies removes the encrypted rows and matching legacy files.
// Migration cannot read legacy files while the deletion transaction is open.
func DeleteTrackerCookies(ctx context.Context, dbPath string, trackerID string, expectedOwner ...string) error {
	if ctx == nil {
		return errors.New("cookies: context is required")
	}

	normalizedTrackerID := strings.TrimSpace(trackerID)
	if normalizedTrackerID == "" {
		return errors.New("cookies: tracker id is required")
	}

	var removedLegacy []legacyCookieCandidateSnapshot
	removeFiles := func() error {
		candidates, err := legacyTrackerCookiePaths(dbPath, normalizedTrackerID)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			snapshot, removed, err := removeLegacyCookieCandidate(candidate)
			if err != nil {
				deleteErr := fmt.Errorf("cookies: delete tracker %s legacy cookie file %s: %w", normalizedTrackerID, candidate, err)
				restoreErr := restoreLegacyCookieCandidates(removedLegacy)
				removedLegacy = nil
				return errors.Join(deleteErr, restoreErr)
			}
			if removed {
				removedLegacy = append(removedLegacy, snapshot)
			}
		}
		return nil
	}
	store, _, repo, err := openTrackerCookieStore(ctx, dbPath)
	if err == nil {
		defer func() {
			_ = repo.Close()
		}()
		err = store.RunInTransaction(ctx, func(tx *sql.Tx) error {
			if err := store.DeleteAllTrackerCookiesTx(ctx, tx, normalizedTrackerID); err != nil {
				return fmt.Errorf("cookies: delete tracker %s from db: %w", normalizedTrackerID, err)
			}
			return removeFiles()
		})
	} else if errors.Is(err, ErrAuthHelperUnavailable) {
		if err = allowLegacyCookieFallback(ctx, dbPath, normalizedTrackerID, expectedOwner); err == nil {
			err = removeFiles()
		}
	}
	if err != nil {
		return errors.Join(err, restoreLegacyCookieCandidates(removedLegacy))
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
	for _, snapshot := range slices.Backward(snapshots) {
		if err := snapshot.restore(); err != nil {
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

func hasManagedTrackerCookies(ctx context.Context, db *sql.DB, trackerID string) (bool, error) {
	var managed bool
	err := db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM tracker_cookies WHERE tracker_id = ? AND cookie_name = ?)",
		trackerID, trackerCookieOwnerKey,
	).Scan(&managed)
	if err != nil {
		return false, fmt.Errorf("cookies: read managed session marker: %w", err)
	}
	return managed, nil
}

func allowLegacyCookieFallback(ctx context.Context, dbPath, trackerID string, expectedOwner []string) error {
	if len(expectedOwner) != 1 || expectedOwner[0] == "" {
		return nil
	}
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		return fmt.Errorf("cookies: open managed session storage: %w", err)
	}
	defer func() { _ = repo.Close() }()
	managed, err := hasManagedTrackerCookies(ctx, repo.RawDB(), trackerID)
	if isMissingCookieSchemaError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if managed {
		return ErrAuthHelperUnavailable
	}
	return nil
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
	candidates, err := legacyTrackerCookiePaths(dbPath, trackerID)
	if err != nil {
		return nil, err
	}
	var firstLoadErr error
	for _, candidate := range candidates {
		switch strings.ToLower(filepath.Ext(candidate)) {
		case ".txt":
			cookies, err := commonhttp.LoadNetscapeCookies(candidate, "")
			if err != nil {
				if !isIgnorableLegacyNetscapeLoadErr(err) && firstLoadErr == nil {
					firstLoadErr = fmt.Errorf("cookies: load legacy cookie file %s: %w", candidate, err)
				}
				continue
			}
			values := httpCookiesToMap(cookies)
			if len(values) > 0 {
				return values, nil
			}
		case ".json":
			values, err := commonhttp.LoadJSONCookieMap(candidate)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) && firstLoadErr == nil {
					firstLoadErr = fmt.Errorf("cookies: load legacy cookie file %s: %w", candidate, err)
				}
				continue
			}
			if len(values) > 0 {
				return values, nil
			}
		}
	}
	if firstLoadErr != nil {
		return nil, firstLoadErr
	}

	return nil, fmt.Errorf("%w: no cookies found for tracker %s", ErrTrackerCookiesNotFound, trackerID)
}

func loadTrackerHTTPCookiesFromFiles(ctx context.Context, dbPath string, trackerID string, domain string) ([]*http.Cookie, error) {
	candidates, err := legacyTrackerCookiePaths(dbPath, trackerID)
	if err != nil {
		return nil, err
	}
	var firstLoadErr error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("cookies: legacy files: %w", err)
		}
		switch strings.ToLower(filepath.Ext(candidate)) {
		case ".txt":
			cookies, err := commonhttp.LoadNetscapeCookies(candidate, domain)
			if ctx.Err() != nil {
				return nil, fmt.Errorf("cookies: legacy files: %w", ctx.Err())
			}
			if err != nil {
				if !isIgnorableLegacyNetscapeLoadErr(err) && firstLoadErr == nil {
					firstLoadErr = fmt.Errorf("cookies: load legacy cookie file %s: %w", candidate, err)
				}
				continue
			}
			if len(cookies) > 0 {
				return cookies, nil
			}
		case ".json":
			values, err := commonhttp.LoadJSONCookieMap(candidate)
			if ctx.Err() != nil {
				return nil, fmt.Errorf("cookies: legacy files: %w", ctx.Err())
			}
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) && firstLoadErr == nil {
					firstLoadErr = fmt.Errorf("cookies: load legacy cookie file %s: %w", candidate, err)
				}
				continue
			}
			cookies := CookieMapToHTTPCookies(values, domain)
			if len(cookies) > 0 {
				return cookies, nil
			}
		}
	}
	if firstLoadErr != nil {
		return nil, firstLoadErr
	}

	return nil, fmt.Errorf("%w: no cookies found for tracker %s", ErrTrackerCookiesNotFound, trackerID)
}

// isIgnorableLegacyNetscapeLoadErr reports whether a legacy Netscape file
// failed for the same reasons as a missing candidate: absent file or no cookies
// matching the requested import.
func isIgnorableLegacyNetscapeLoadErr(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return strings.Contains(err.Error(), "no valid cookies found")
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
