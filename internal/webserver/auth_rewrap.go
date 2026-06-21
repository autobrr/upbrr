// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
)

func generateStableEncryptionSeed() (string, error) {
	return wrapWebResult(authmaterial.GenerateSeed())
}

// rewrapProtectedDataForAuthChange resumes pending auth upgrades by advancing
// through cookie and config-secret rewrap phases. If a previous prepared-phase
// attempt already re-encrypted cookies but failed before persisting the phase,
// the cookie phase verifies readability with the target auth material and
// records the recovered cookie auth state before continuing.
func (s *Server) rewrapProtectedDataForAuthChange(ctx context.Context, oldRecord, newRecord authRecord) error {
	if s == nil || s.backend == nil || s.backend.repo == nil {
		return errors.New("auth_rewrap: missing server/backend/repo configuration (s.backend.repo unavailable)")
	}

	if oldRecord.PendingUpgrade == nil {
		if err := s.auth.BeginPendingUpgrade(oldRecord, newRecord); err != nil {
			return fmt.Errorf("auth rewrap: persist pending upgrade: %w", err)
		}
		oldRecord.PendingUpgrade = &authmaterial.PendingUpgrade{
			Stage:     authmaterial.UpgradeStagePrepared,
			Target:    newRecord,
			UpdatedAt: time.Now().UTC(),
		}
	}

	pending := oldRecord.PendingUpgrade
	if pending == nil {
		return errors.New("auth rewrap: missing pending upgrade state")
	}
	newRecord = pending.Target
	oldMaterial := oldRecord.AuthMaterial()
	newMaterial := newRecord.AuthMaterial()

	if pending.Stage == "" {
		pending.Stage = authmaterial.UpgradeStagePrepared
	}

	if pending.Stage == authmaterial.UpgradeStagePrepared {
		if err := rewrapCookiesForAuthChange(ctx, s.backend.repo.RawDB(), oldMaterial, newMaterial); err != nil {
			return fmt.Errorf("web: %w", err)
		}
		if err := s.auth.AdvancePendingUpgrade(oldRecord.Username, authmaterial.UpgradeStageCookiesRewrapped); err != nil {
			return fmt.Errorf("auth rewrap: persist cookie phase: %w", err)
		}
		pending.Stage = authmaterial.UpgradeStageCookiesRewrapped
	}

	if pending.Stage == authmaterial.UpgradeStageCookiesRewrapped {
		sourceMaterials := []authmaterial.Material{oldMaterial, newMaterial}
		if err := config.RewrapSecretsInDatabaseWithFallback(ctx, s.backend.repo, sourceMaterials, newMaterial); err != nil {
			return fmt.Errorf("web: %w", err)
		}
		if err := s.auth.AdvancePendingUpgrade(oldRecord.Username, authmaterial.UpgradeStageDataRewrapped); err != nil {
			return fmt.Errorf("auth rewrap: persist data phase: %w", err)
		}
		pending.Stage = authmaterial.UpgradeStageDataRewrapped
	}

	return nil
}

// rewrapCookiesForAuthChange retries an interrupted prepared-phase cookie
// upgrade by accepting already-rewrapped cookies only after verifying they
// decrypt with the target auth material.
func rewrapCookiesForAuthChange(ctx context.Context, db *sql.DB, oldMaterial, newMaterial authmaterial.Material) error {
	err := cookies.RewrapCookiesWithAuthChange(ctx, db, oldMaterial, newMaterial)
	if err == nil {
		return nil
	}
	if !isCookieRewrapDecryptFailure(err) {
		return fmt.Errorf("auth rewrap: rewrap cookies: %w", err)
	}

	if verifyErr := verifyCookiesReadableWithAuthMaterial(ctx, db, newMaterial); verifyErr != nil {
		return errors.Join(err, fmt.Errorf("auth rewrap: verify recovered cookies: %w", verifyErr))
	}
	if markerErr := cookies.RewrapCookiesWithAuthChange(ctx, db, newMaterial, newMaterial); markerErr != nil {
		return fmt.Errorf("auth rewrap: persist recovered cookie auth state: %w", markerErr)
	}

	return nil
}

func isCookieRewrapDecryptFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "failed to decrypt cookie")
}

// verifyCookiesReadableWithAuthMaterial checks every encrypted cookie row
// against material without modifying cookie data or auth-state metadata.
func verifyCookiesReadableWithAuthMaterial(ctx context.Context, db *sql.DB, material authmaterial.Material) error {
	if ctx == nil {
		return cookies.ErrNilContext
	}
	if db == nil {
		return errors.New("cookies: database connection is required")
	}

	helper, _, err := material.PrimaryHelper()
	if err != nil {
		return fmt.Errorf("cookies: derive auth helper: %w", err)
	}

	salt, err := loadCookieEncryptionSalt(ctx, db)
	if err != nil {
		return err
	}
	key, err := cookies.DeriveEncryptionKey(helper, salt)
	if err != nil {
		return fmt.Errorf("cookies: derive encryption key: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT tracker_id, cookie_name, encrypted_value, nonce, auth_tag FROM tracker_cookies`)
	if err != nil {
		return fmt.Errorf("cookies: query encrypted cookies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var trackerID, cookieName string
		var encoded cookies.EncodedEncryptedCookie
		if err := rows.Scan(&trackerID, &cookieName, &encoded.CiphertextB64, &encoded.NonceB64, &encoded.AuthTagB64); err != nil {
			return fmt.Errorf("cookies: scan encrypted cookie: %w", err)
		}

		encrypted, err := cookies.DecodeFromStorage(encoded)
		if err != nil {
			return fmt.Errorf("cookies: decode recovered cookie for tracker %s cookie %s: %w", trackerID, cookieName, err)
		}
		if _, err := cookies.DecryptCookieValue(encrypted, key); err != nil {
			return fmt.Errorf("cookies: decrypt recovered cookie for tracker %s cookie %s: %w", trackerID, cookieName, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cookies: iterate encrypted cookies: %w", err)
	}

	return nil
}

// loadCookieEncryptionSalt loads the stored cookie encryption salt required to
// derive a key outside the normal rewrap transaction.
func loadCookieEncryptionSalt(ctx context.Context, db *sql.DB) (string, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT data FROM config_settings WHERE section = ?`, "cookies_encryption_salt").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("cookies: encryption salt missing")
	}
	if err != nil {
		return "", fmt.Errorf("cookies: query encryption salt: %w", err)
	}

	var payload struct {
		Salt string `json:"salt"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("cookies: parse encryption salt: %w", err)
	}

	salt := strings.TrimSpace(payload.Salt)
	if salt == "" {
		return "", errors.New("cookies: encryption salt missing")
	}

	return salt, nil
}
