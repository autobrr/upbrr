// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CookieStore provides database access for encrypted cookies.
type CookieStore struct {
	db *sql.DB
}

// NewCookieStore creates a new CookieStore instance.
func NewCookieStore(db *sql.DB) (*CookieStore, error) {
	if db == nil {
		return nil, errors.New("cookies: nil database connection")
	}

	return &CookieStore{db: db}, nil
}

// SaveCookie saves or updates an encrypted cookie in the database.
func (cs *CookieStore) SaveCookie(ctx context.Context, trackerID, cookieName, cookieValue string, key []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if trackerID == "" || cookieName == "" {
		return errors.New("SaveCookie: trackerID and cookieName must be non-empty")
	}

	// Encrypt the cookie value
	encrypted, err := EncryptCookieValue(cookieValue, key)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	encoded := encrypted.EncodeForStorage()

	query := `
		INSERT INTO tracker_cookies (tracker_id, cookie_name, encrypted_value, nonce, auth_tag, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(tracker_id, cookie_name) DO UPDATE SET
			encrypted_value = excluded.encrypted_value,
			nonce = excluded.nonce,
			auth_tag = excluded.auth_tag,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err = cs.db.ExecContext(ctx, query, trackerID, cookieName, encoded.CiphertextB64, encoded.NonceB64, encoded.AuthTagB64)
	if err != nil {
		return fmt.Errorf("failed to save cookie: %w", err)
	}

	return nil
}

// GetCookie retrieves and decrypts a single cookie from the database.
func (cs *CookieStore) GetCookie(ctx context.Context, trackerID, cookieName string, key []byte) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	query := `SELECT encrypted_value, nonce, auth_tag FROM tracker_cookies WHERE tracker_id = ? AND cookie_name = ?`

	var ciphertextB64, nonceB64, authTagB64 string
	err := cs.db.QueryRowContext(ctx, query, trackerID, cookieName).Scan(&ciphertextB64, &nonceB64, &authTagB64)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // Cookie not found
	}
	if err != nil {
		return "", fmt.Errorf("database query failed: %w", err)
	}

	encoded := EncodedEncryptedCookie{
		CiphertextB64: ciphertextB64,
		NonceB64:      nonceB64,
		AuthTagB64:    authTagB64,
	}

	encrypted, err := DecodeFromStorage(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode stored cookie: %w", err)
	}

	plaintext, err := DecryptCookieValue(encrypted, key)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// GetAllTrackerCookies retrieves and decrypts all cookies for a tracker.
// Returns a map[cookieName]cookieValue.
func (cs *CookieStore) GetAllTrackerCookies(ctx context.Context, trackerID string, key []byte) (map[string]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	query := `SELECT cookie_name, encrypted_value, nonce, auth_tag FROM tracker_cookies WHERE tracker_id = ?`

	rows, err := cs.db.QueryContext(ctx, query, trackerID)
	if err != nil {
		return nil, fmt.Errorf("database query failed: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var cookieName, ciphertextB64, nonceB64, authTagB64 string
		if err := rows.Scan(&cookieName, &ciphertextB64, &nonceB64, &authTagB64); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		encoded := EncodedEncryptedCookie{
			CiphertextB64: ciphertextB64,
			NonceB64:      nonceB64,
			AuthTagB64:    authTagB64,
		}

		encrypted, err := DecodeFromStorage(encoded)
		if err != nil {
			return nil, fmt.Errorf("failed to decode stored cookie '%s': %w", cookieName, err)
		}

		plaintext, err := DecryptCookieValue(encrypted, key)
		if err != nil {
			return nil, fmt.Errorf("decryption failed for cookie '%s': %w", cookieName, err)
		}

		result[cookieName] = plaintext
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return result, nil
}

// DeleteCookie removes a specific cookie from the database.
func (cs *CookieStore) DeleteCookie(ctx context.Context, trackerID, cookieName string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	query := `DELETE FROM tracker_cookies WHERE tracker_id = ? AND cookie_name = ?`
	_, err := cs.db.ExecContext(ctx, query, trackerID, cookieName)
	if err != nil {
		return fmt.Errorf("failed to delete cookie: %w", err)
	}

	return nil
}

// DeleteAllTrackerCookies removes all cookies for a tracker.
func (cs *CookieStore) DeleteAllTrackerCookies(ctx context.Context, trackerID string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	query := `DELETE FROM tracker_cookies WHERE tracker_id = ?`
	_, err := cs.db.ExecContext(ctx, query, trackerID)
	if err != nil {
		return fmt.Errorf("failed to delete cookies for tracker: %w", err)
	}

	return nil
}

// HasCookies checks if a tracker has any cookies in the database.
func (cs *CookieStore) HasCookies(ctx context.Context, trackerID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var count int
	query := `SELECT COUNT(1) FROM tracker_cookies WHERE tracker_id = ?`
	if err := cs.db.QueryRowContext(ctx, query, trackerID).Scan(&count); err != nil {
		return false, fmt.Errorf("database query failed: %w", err)
	}

	return count > 0, nil
}
