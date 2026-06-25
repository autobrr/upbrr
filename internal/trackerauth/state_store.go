// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

var ErrAuthStateNotFound = errors.New("tracker auth state not found")

type AuthStateStore struct {
	db *sql.DB
}

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

func DeleteAuthState(ctx context.Context, dbPath string, trackerID string, stateKey string) error {
	store, _, repo, err := openAuthStateStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = repo.Close()
	}()
	return store.Delete(ctx, trackerID, stateKey)
}

func NewAuthStateStore(db *sql.DB) (*AuthStateStore, error) {
	if db == nil {
		return nil, errors.New("tracker auth state: nil database connection")
	}
	return &AuthStateStore{db: db}, nil
}

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
	encrypted, err := cookiepkg.EncryptCookieValue(value, encKey)
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
	`, normalizeTrackerID(trackerID), strings.TrimSpace(stateKey), encoded.CiphertextB64, encoded.NonceB64, encoded.AuthTagB64)
	if err != nil {
		return fmt.Errorf("tracker auth state: save: %w", err)
	}
	return nil
}

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
	var encoded cookiepkg.EncodedEncryptedCookie
	err := s.db.QueryRowContext(ctx, `
		SELECT encrypted_value, nonce, auth_tag
		FROM tracker_auth_state
		WHERE tracker_id = ? AND state_key = ?
	`, normalizeTrackerID(trackerID), strings.TrimSpace(stateKey)).Scan(&encoded.CiphertextB64, &encoded.NonceB64, &encoded.AuthTagB64)
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
	value, err := cookiepkg.DecryptCookieValue(encrypted, encKey)
	if err != nil {
		return "", fmt.Errorf("tracker auth state: decrypt: %w", err)
	}
	return value, nil
}

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

func openAuthStateStore(ctx context.Context, dbPath string) (*AuthStateStore, []byte, *servicedb.SQLiteRepository, error) {
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("tracker auth state: open db: %w", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		return nil, nil, nil, fmt.Errorf("tracker auth state: migrate db: %w", err)
	}
	store, err := NewAuthStateStore(repo.RawDB())
	if err != nil {
		_ = repo.Close()
		return nil, nil, nil, err
	}
	encKey, err := cookiepkg.NewKeyManager(repo.RawDB()).InitializeEncryptionKey(ctx, dbPath)
	if err != nil {
		_ = repo.Close()
		return nil, nil, nil, fmt.Errorf("tracker auth state: initialize encryption key: %w", err)
	}
	return store, encKey, repo, nil
}

func validateStateInputs(operation string, trackerID string, stateKey string) error {
	if strings.TrimSpace(trackerID) == "" || strings.TrimSpace(stateKey) == "" {
		return fmt.Errorf("%s: trackerID and stateKey must be non-empty", operation)
	}
	return nil
}
