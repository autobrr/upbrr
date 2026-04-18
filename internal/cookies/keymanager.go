// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

const (
	// cookieKeyConfigSection is the section name in config_settings where we store the salt.
	cookieKeyConfigSection       = "cookies_encryption_salt"
	cookieAuthStateConfigSection = "cookies_encryption_auth_state"
)

var (
	// ErrAuthHelperUnavailable indicates no existing web auth record can be used for key derivation.
	ErrAuthHelperUnavailable = errors.New("cookie encryption auth helper unavailable")
	// ErrNilContext indicates a nil context was passed to a key manager API that requires one.
	ErrNilContext = errors.New("cookies: nil context provided")
)

type authState struct {
	Fingerprint       string `json:"fingerprint"`
	legacyHelperFound bool   `json:"-"`
}

type authHelperCandidate struct {
	Helper      string
	Fingerprint string
}

// KeyManager manages the encryption key for cookies.
type KeyManager struct {
	db *sql.DB
}

// NewKeyManager creates a new KeyManager instance.
func NewKeyManager(db *sql.DB) *KeyManager {
	if db == nil {
		panic("nil db passed to NewKeyManager")
	}

	return &KeyManager{
		db: db,
	}
}

// InitializeEncryptionKey derives the cookie encryption key from existing web auth details.
// ctx must be non-nil.
// and transparently rotates encrypted cookie rows when the source auth changes.
func (km *KeyManager) InitializeEncryptionKey(ctx context.Context, dbPath string) ([]byte, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	salt, err := ensureSalt(ctx, km.db)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure salt: %w", err)
	}

	helpers, err := loadAuthHelpers(dbPath)
	if err != nil {
		if errors.Is(err, ErrAuthHelperUnavailable) {
			return nil, ErrAuthHelperUnavailable
		}
		return nil, fmt.Errorf("failed to load auth helper: %w", err)
	}
	currentHelper := helpers[0].Helper
	currentFingerprint := helpers[0].Fingerprint

	state, err := getAuthStateFromDB(ctx, km.db)
	if err != nil {
		return nil, fmt.Errorf("failed to load auth state: %w", err)
	}

	var key []byte

	switch {
	case state.Fingerprint == "":
		if err := storeAuthStateInDB(ctx, km.db, authState{Fingerprint: currentFingerprint}); err != nil {
			return nil, fmt.Errorf("failed to store initial auth state: %w", err)
		}
	case state.Fingerprint != currentFingerprint:
		oldHelper, ok := findAuthHelperByFingerprint(helpers, state.Fingerprint)
		if !ok {
			return nil, ErrAuthHelperUnavailable
		}

		oldKey, err := DeriveEncryptionKey(oldHelper, salt)
		if err != nil {
			return nil, fmt.Errorf("failed to derive old encryption key: %w", err)
		}

		newKey, err := DeriveEncryptionKey(currentHelper, salt)
		if err != nil {
			return nil, fmt.Errorf("failed to derive new encryption key: %w", err)
		}

		if err := km.reencryptCookies(ctx, oldKey, newKey); err != nil {
			return nil, fmt.Errorf("failed to re-encrypt cookies after auth update: %w", err)
		}

		if err := storeAuthStateInDB(ctx, km.db, authState{Fingerprint: currentFingerprint}); err != nil {
			return nil, fmt.Errorf("failed to persist updated auth state: %w", err)
		}

		key = newKey
	case state.legacyHelperFound:
		if err := storeAuthStateInDB(ctx, km.db, authState{Fingerprint: currentFingerprint}); err != nil {
			return nil, fmt.Errorf("failed to normalize auth state: %w", err)
		}
	}

	if key == nil {
		key, err = DeriveEncryptionKey(currentHelper, salt)
		if err != nil {
			return nil, fmt.Errorf("failed to derive key: %w", err)
		}
	}

	return key, nil
}

func ensureSalt(ctx context.Context, db *sql.DB) (string, error) {
	salt, err := getSaltFromDB(ctx, db)
	if err != nil {
		return "", err
	}
	if salt != "" {
		return salt, nil
	}

	saltBytes, err := GenerateRandomBytes(16)
	if err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}
	newSalt := hex.EncodeToString(saltBytes)
	if err := storeSaltInDB(ctx, db, newSalt); err != nil {
		return "", err
	}

	return newSalt, nil
}

// loadAuthHelpers returns candidate auth helpers derived from web-auth.json.
// The first candidate is always the current primary helper; when available,
// a legacy helper candidate is included so older fingerprints can be matched.
// Returns ErrAuthHelperUnavailable when the auth file is unavailable.
func loadAuthHelpers(dbPath string) ([]authHelperCandidate, error) {
	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return nil, ErrAuthHelperUnavailable
		}
		return nil, fmt.Errorf("failed loading web auth helper: %w", err)
	}

	primaryHelper, primaryFingerprint, err := material.PrimaryHelper()
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return nil, ErrAuthHelperUnavailable
		}
		return nil, fmt.Errorf("failed deriving primary web auth helper: %w", err)
	}

	candidates := []authHelperCandidate{{
		Helper:      primaryHelper,
		Fingerprint: primaryFingerprint,
	}}

	if legacyHelper, legacyFingerprint, legacyErr := material.LegacyHelper(); legacyErr == nil && legacyFingerprint != primaryFingerprint {
		candidates = append(candidates, authHelperCandidate{
			Helper:      legacyHelper,
			Fingerprint: legacyFingerprint,
		})
	}

	return candidates, nil
}

func RewrapCookiesWithAuthChange(ctx context.Context, db *sql.DB, oldMaterial, newMaterial authmaterial.Material) error {
	if ctx == nil {
		return ErrNilContext
	}
	if db == nil {
		return errors.New("cookies: database connection is required")
	}

	oldHelper, oldFingerprint, err := oldMaterial.PrimaryHelper()
	if err != nil {
		return fmt.Errorf("cookies: derive old auth helper: %w", err)
	}
	newHelper, newFingerprint, err := newMaterial.PrimaryHelper()
	if err != nil {
		return fmt.Errorf("cookies: derive new auth helper: %w", err)
	}

	km := NewKeyManager(db)
	salt, err := ensureSalt(ctx, db)
	if err != nil {
		return fmt.Errorf("cookies: ensure salt: %w", err)
	}

	if oldFingerprint != newFingerprint {
		oldKey, err := DeriveEncryptionKey(oldHelper, salt)
		if err != nil {
			return fmt.Errorf("cookies: derive old encryption key: %w", err)
		}
		newKey, err := DeriveEncryptionKey(newHelper, salt)
		if err != nil {
			return fmt.Errorf("cookies: derive new encryption key: %w", err)
		}
		if err := km.reencryptCookies(ctx, oldKey, newKey); err != nil {
			return fmt.Errorf("cookies: re-encrypt cookies after auth update: %w", err)
		}
	}

	if err := storeAuthStateInDB(ctx, db, authState{Fingerprint: newFingerprint}); err != nil {
		return fmt.Errorf("cookies: store auth state: %w", err)
	}

	return nil
}

func (km *KeyManager) reencryptCookies(ctx context.Context, oldKey, newKey []byte) error {
	if ctx == nil {
		return ErrNilContext
	}

	tx, err := km.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	rows, err := tx.QueryContext(ctx, `SELECT tracker_id, cookie_name, encrypted_value, nonce, auth_tag FROM tracker_cookies`)
	if err != nil {
		return fmt.Errorf("failed to query cookies: %w", err)
	}
	defer rows.Close()

	type cookieRow struct {
		trackerID     string
		cookieName    string
		ciphertextB64 string
		nonceB64      string
		authTagB64    string
	}

	var items []cookieRow
	for rows.Next() {
		var item cookieRow
		if err := rows.Scan(&item.trackerID, &item.cookieName, &item.ciphertextB64, &item.nonceB64, &item.authTagB64); err != nil {
			return fmt.Errorf("failed to scan cookie row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed iterating cookie rows: %w", err)
	}

	for _, item := range items {
		encrypted, err := DecodeFromStorage(EncodedEncryptedCookie{
			CiphertextB64: item.ciphertextB64,
			NonceB64:      item.nonceB64,
			AuthTagB64:    item.authTagB64,
		})
		if err != nil {
			return fmt.Errorf("failed to decode encrypted cookie for tracker %s cookie %s: %w", item.trackerID, item.cookieName, err)
		}

		plaintext, err := DecryptCookieValue(encrypted, oldKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt cookie for tracker %s cookie %s: %w", item.trackerID, item.cookieName, err)
		}

		reencrypted, err := EncryptCookieValue(plaintext, newKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt cookie for tracker %s cookie %s: %w", item.trackerID, item.cookieName, err)
		}

		encoded := reencrypted.EncodeForStorage()
		if _, err := tx.ExecContext(ctx, `
			UPDATE tracker_cookies
			SET encrypted_value = ?, nonce = ?, auth_tag = ?, updated_at = CURRENT_TIMESTAMP
			WHERE tracker_id = ? AND cookie_name = ?
		`, encoded.CiphertextB64, encoded.NonceB64, encoded.AuthTagB64, item.trackerID, item.cookieName); err != nil {
			return fmt.Errorf("failed to update re-encrypted cookie for tracker %s cookie %s: %w", item.trackerID, item.cookieName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit cookie re-encryption transaction: %w", err)
	}

	return nil
}

func getAuthStateFromDB(ctx context.Context, db *sql.DB) (authState, error) {
	if ctx == nil {
		return authState{}, ErrNilContext
	}

	var jsonData string
	err := db.QueryRowContext(ctx, `SELECT data FROM config_settings WHERE section = ?`, cookieAuthStateConfigSection).Scan(&jsonData)
	if errors.Is(err, sql.ErrNoRows) {
		return authState{}, nil
	}
	if err != nil {
		return authState{}, fmt.Errorf("failed to query auth state: %w", err)
	}

	var raw struct {
		Helper      string `json:"helper"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		return authState{}, fmt.Errorf("failed to parse auth state JSON: %w", err)
	}

	return authState{
		Fingerprint:       strings.TrimSpace(raw.Fingerprint),
		legacyHelperFound: strings.TrimSpace(raw.Helper) != "",
	}, nil
}

func storeAuthStateInDB(ctx context.Context, db *sql.DB, state authState) error {
	if ctx == nil {
		return ErrNilContext
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal auth state: %w", err)
	}

	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO config_settings (section, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`, cookieAuthStateConfigSection, string(payload))
	if err != nil {
		return fmt.Errorf("failed to save auth state: %w", err)
	}

	return nil
}

func findAuthHelperByFingerprint(helpers []authHelperCandidate, fingerprint string) (string, bool) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return "", false
	}

	for _, helper := range helpers {
		if strings.TrimSpace(helper.Fingerprint) == fingerprint {
			return helper.Helper, true
		}
	}

	return "", false
}

// getSaltFromDB retrieves the encryption salt from the database.
func getSaltFromDB(ctx context.Context, db *sql.DB) (string, error) {
	if ctx == nil {
		return "", ErrNilContext
	}

	var jsonData string
	query := `SELECT data FROM config_settings WHERE section = ?`
	err := db.QueryRowContext(ctx, query, cookieKeyConfigSection).Scan(&jsonData)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // No salt exists yet
	}
	if err != nil {
		return "", fmt.Errorf("failed to query database: %w", err)
	}

	var raw struct {
		Salt string `json:"salt"`
	}
	if err := json.Unmarshal([]byte(jsonData), &raw); err != nil {
		return "", fmt.Errorf("failed to parse JSON from database: %w", err)
	}

	if strings.TrimSpace(raw.Salt) != "" {
		return strings.TrimSpace(raw.Salt), nil
	}

	return "", nil // No salt in the data
}

// storeSaltInDB stores the encryption salt in the database.
func storeSaltInDB(ctx context.Context, db *sql.DB, salt string) error {
	if ctx == nil {
		return ErrNilContext
	}

	data := map[string]interface{}{
		"salt": salt,
	}

	saltJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal salt: %w", err)
	}

	query := `INSERT OR REPLACE INTO config_settings (section, data, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)`
	_, err = db.ExecContext(ctx, query, cookieKeyConfigSection, string(saltJSON))
	if err != nil {
		return fmt.Errorf("failed to save salt to database: %w", err)
	}

	return nil
}
