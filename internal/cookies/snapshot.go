// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SnapshotTrackerCookieMap reads cookies from an already-open server database
// and its trusted legacy location without opening, migrating, initializing or
// rotating storage. Legacy Netscape cookies are filtered to domain before
// conversion; stored values override legacy values as in LoadTrackerHTTPCookies.
// Usable legacy cookies remain available when stored key material is absent;
// a known auth-helper fingerprint mismatch still rejects the entire snapshot.
// Cancellation is checked between reads but cannot interrupt OS-blocked file I/O.
// Connection tests run this under bounded storage admission and stop waiting
// when canceled without starting replacement readers.
// The returned map is detached from persistence and must never be logged.
func SnapshotTrackerCookieMap(ctx context.Context, db *sql.DB, dbPath, trackerID, domain string) (map[string]string, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if db == nil {
		return nil, errors.New("cookies: snapshot requires an open database")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cookies: snapshot: %w", err)
	}
	fileCookies, fileErr := loadTrackerHTTPCookiesFromFiles(ctx, dbPath, trackerID, domain)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cookies: snapshot: %w", err)
	}
	fileValues := httpCookiesToMap(fileCookies)
	var present bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tracker_cookies WHERE tracker_id = ?)`, trackerID).Scan(&present); err != nil {
		return nil, fmt.Errorf("cookies: snapshot presence: %w", err)
	}
	if !present {
		return fileValues, fileErr
	}
	salt, err := getSaltFromDB(ctx, db)
	if err != nil {
		return nil, err
	}
	state, err := getAuthStateFromDB(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("cookies: snapshot: %w", err)
	}
	helpers, err := loadAuthHelpers(dbPath)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("cookies: snapshot: %w", ctx.Err())
	}
	if err != nil {
		if errors.Is(err, ErrAuthHelperUnavailable) && len(fileValues) > 0 {
			return fileValues, nil
		}
		return nil, err
	}
	helper := helpers[0].Helper
	if state.Fingerprint != "" {
		var ok bool
		helper, ok = findAuthHelperByFingerprint(helpers, state.Fingerprint)
		if !ok {
			return nil, ErrAuthHelperUnavailable
		}
	}
	if salt == "" {
		if len(fileValues) > 0 {
			return fileValues, nil
		}
		return nil, ErrAuthHelperUnavailable
	}
	key, err := DeriveEncryptionKey(helper, salt)
	if err != nil {
		return nil, err
	}
	store, err := NewCookieStore(db)
	if err != nil {
		return nil, err
	}
	values, err := store.GetAllTrackerCookies(ctx, trackerID, key)
	if err != nil {
		return nil, err
	}
	return mergeCookieMaps(fileValues, values), nil
}
