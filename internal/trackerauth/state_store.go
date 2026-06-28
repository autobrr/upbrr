// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// ErrAuthStateNotFound is returned when no encrypted tracker auth state exists for a tracker/key pair.
var ErrAuthStateNotFound = errors.New("tracker auth state not found")

// AuthStateStore persists encrypted tracker auth state in the shared SQLite database.
type AuthStateStore struct {
	db *sql.DB
}

// authStatePayload binds encrypted auth state to the row identity used to load it.
type authStatePayload struct {
	TrackerID string `json:"tracker_id"`
	StateKey  string `json:"state_key"`
	Value     string `json:"value"`
}

// SaveAuthState opens dbPath, initializes encrypted auth storage, and upserts value for trackerID/stateKey.
func SaveAuthState(ctx context.Context, dbPath string, trackerID string, stateKey string, value string) error {
	store, encKey, repo, err := openAuthStateStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = repo.Close()
	}()
	return store.Save(ctx, trackerID, stateKey, value, encKey)
}

// LoadAuthState opens dbPath and decrypts value for trackerID/stateKey.
// It returns [ErrAuthStateNotFound] when the key has not been saved.
func LoadAuthState(ctx context.Context, dbPath string, trackerID string, stateKey string) (string, error) {
	store, encKey, repo, err := openAuthStateStore(ctx, dbPath)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = repo.Close()
	}()
	return store.Load(ctx, trackerID, stateKey, encKey)
}

// DeleteAuthState opens dbPath and deletes value for trackerID/stateKey.
func DeleteAuthState(ctx context.Context, dbPath string, trackerID string, stateKey string) error {
	store, repo, err := openAuthStateStoreWithoutKey(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = repo.Close()
	}()
	return store.Delete(ctx, trackerID, stateKey)
}

// NewAuthStateStore wraps db for encrypted tracker auth state operations.
func NewAuthStateStore(db *sql.DB) (*AuthStateStore, error) {
	if db == nil {
		return nil, errors.New("tracker auth state: nil database connection")
	}
	return &AuthStateStore{db: db}, nil
}

// Save encrypts value with encKey and upserts it for trackerID/stateKey.
// encKey must be a 32-byte key from cookie encryption storage.
func (s *AuthStateStore) Save(ctx context.Context, trackerID string, stateKey string, value string, encKey []byte) error {
	if ctx == nil {
		return errors.New("tracker auth state: context is required")
	}
	if err := validateStateInputs("Save", trackerID, stateKey); err != nil {
		return err
	}
	if len(encKey) != 32 {
		return errors.New("Save: invalid encryption key")
	}
	normalizedTrackerID := normalizeTrackerID(trackerID)
	normalizedStateKey := strings.TrimSpace(stateKey)
	encrypted, err := encryptAuthStateValue(value, encKey, normalizedTrackerID, normalizedStateKey)
	if err != nil {
		return fmt.Errorf("tracker auth state: encrypt: %w", err)
	}
	encoded := encrypted.EncodeForStorage()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tracker_auth_state (tracker_id, state_key, encrypted_value, nonce, auth_tag, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(tracker_id, state_key) DO UPDATE SET
			encrypted_value = excluded.encrypted_value,
			nonce = excluded.nonce,
			auth_tag = excluded.auth_tag,
			updated_at = CURRENT_TIMESTAMP
	`, normalizedTrackerID, normalizedStateKey, encoded.CiphertextB64, encoded.NonceB64, encoded.AuthTagB64)
	if err != nil {
		return fmt.Errorf("tracker auth state: save: %w", err)
	}
	return nil
}

// Load decrypts value for trackerID/stateKey with encKey.
// It returns [ErrAuthStateNotFound] when the key has not been saved.
func (s *AuthStateStore) Load(ctx context.Context, trackerID string, stateKey string, encKey []byte) (string, error) {
	if ctx == nil {
		return "", errors.New("tracker auth state: context is required")
	}
	if err := validateStateInputs("Load", trackerID, stateKey); err != nil {
		return "", err
	}
	if len(encKey) != 32 {
		return "", errors.New("Load: invalid encryption key")
	}
	normalizedTrackerID := normalizeTrackerID(trackerID)
	normalizedStateKey := strings.TrimSpace(stateKey)
	var encoded cookiepkg.EncodedEncryptedCookie
	err := s.db.QueryRowContext(ctx, `
		SELECT encrypted_value, nonce, auth_tag
		FROM tracker_auth_state
		WHERE tracker_id = ? AND state_key = ?
	`, normalizedTrackerID, normalizedStateKey).Scan(&encoded.CiphertextB64, &encoded.NonceB64, &encoded.AuthTagB64)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAuthStateNotFound
	}
	if err != nil {
		return "", fmt.Errorf("tracker auth state: load: %w", err)
	}
	encrypted, err := cookiepkg.DecodeFromStorage(encoded)
	if err != nil {
		return "", fmt.Errorf("tracker auth state: decode: %w", err)
	}
	value, err := decryptAuthStateValue(encrypted, encKey, normalizedTrackerID, normalizedStateKey)
	if err != nil {
		return "", fmt.Errorf("tracker auth state: decrypt: %w", err)
	}
	return value, nil
}

// Delete removes encrypted value for trackerID/stateKey.
func (s *AuthStateStore) Delete(ctx context.Context, trackerID string, stateKey string) error {
	if ctx == nil {
		return errors.New("tracker auth state: context is required")
	}
	if err := validateStateInputs("Delete", trackerID, stateKey); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tracker_auth_state WHERE tracker_id = ? AND state_key = ?`, normalizeTrackerID(trackerID), strings.TrimSpace(stateKey))
	if err != nil {
		return fmt.Errorf("tracker auth state: delete: %w", err)
	}
	return nil
}

// encryptAuthStateValue stores value with its normalized tracker/state identity so ciphertext cannot be replayed under another row key.
func encryptAuthStateValue(value string, encKey []byte, trackerID string, stateKey string) (*cookiepkg.EncryptedCookie, error) {
	payload, err := json.Marshal(authStatePayload{
		TrackerID: trackerID,
		StateKey:  stateKey,
		Value:     value,
	})
	if err != nil {
		return nil, fmt.Errorf("encode bound payload: %w", err)
	}
	encrypted, err := cookiepkg.EncryptCookieValue(string(payload), encKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt bound payload: %w", err)
	}
	return encrypted, nil
}

// decryptAuthStateValue verifies the encrypted payload identity before returning the stored value.
func decryptAuthStateValue(encrypted *cookiepkg.EncryptedCookie, encKey []byte, trackerID string, stateKey string) (string, error) {
	plaintext, err := cookiepkg.DecryptCookieValue(encrypted, encKey)
	if err != nil {
		return "", fmt.Errorf("decrypt bound payload: %w", err)
	}
	var payload authStatePayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		return "", fmt.Errorf("decode bound payload: %w", err)
	}
	if payload.TrackerID != trackerID || payload.StateKey != stateKey {
		return "", errors.New("encrypted payload identity mismatch")
	}
	return payload.Value, nil
}

func openAuthStateStore(ctx context.Context, dbPath string) (*AuthStateStore, []byte, *servicedb.SQLiteRepository, error) {
	store, repo, err := openAuthStateStoreWithoutKey(ctx, dbPath)
	if err != nil {
		return nil, nil, nil, err
	}
	encKey, err := cookiepkg.NewKeyManager(repo.RawDB()).InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		_ = repo.Close()
		return nil, nil, nil, fmt.Errorf("tracker auth state: initialize encryption key: %w", err)
	}
	return store, encKey, repo, nil
}

// openAuthStateStoreWithoutKey opens and migrates auth-state storage without loading encryption material for delete-only cleanup paths.
func openAuthStateStoreWithoutKey(ctx context.Context, dbPath string) (*AuthStateStore, *servicedb.SQLiteRepository, error) {
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		return nil, nil, fmt.Errorf("tracker auth state: open db: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		return nil, nil, fmt.Errorf("tracker auth state: migrate db: %w", err)
	}
	store, err := NewAuthStateStore(repo.RawDB())
	if err != nil {
		_ = repo.Close()
		return nil, nil, err
	}
	return store, repo, nil
}

func validateStateInputs(operation string, trackerID string, stateKey string) error {
	if strings.TrimSpace(trackerID) == "" || strings.TrimSpace(stateKey) == "" {
		return fmt.Errorf("%s: trackerID and stateKey must be non-empty", operation)
	}
	return nil
}
