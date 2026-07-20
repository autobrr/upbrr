// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/authmaterial"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
)

type keyedFileStateManager struct {
	trackerID      string
	stateKey       string
	legacyFileName string
}

type keyedFileStateSnapshot struct {
	manager        keyedFileStateManager
	dbPath         string
	stateValue     string
	hadState       bool
	legacyPath     string
	legacyValue    []byte
	hadLegacyValue bool
}

// NewKeyedFileStateManager returns a reusable manager for a tracker-owned
// encrypted auth value and optional legacy plaintext file.
func NewKeyedFileStateManager(trackerID string, stateKey string, legacyFileName string) trackers.AuthStateManager {
	return keyedFileStateManager{
		trackerID:      strings.ToUpper(strings.TrimSpace(trackerID)),
		stateKey:       strings.TrimSpace(stateKey),
		legacyFileName: strings.TrimSpace(legacyFileName),
	}
}

func (m keyedFileStateManager) Snapshot(ctx context.Context, dbPath string) (trackers.AuthStateSnapshot, error) {
	snapshot := &keyedFileStateSnapshot{manager: m, dbPath: dbPath}
	stateValue, err := LoadAuthState(ctx, dbPath, m.trackerID, m.stateKey)
	if err == nil {
		snapshot.stateValue = stateValue
		snapshot.hadState = true
	} else if !errors.Is(err, ErrAuthStateNotFound) && encryptedAuthStateMayExist(dbPath) {
		return nil, fmt.Errorf("tracker auth: snapshot %s auth state: %w", m.trackerID, err)
	}
	if m.legacyFileName == "" {
		return snapshot, nil
	}
	legacyPath, err := servicedb.CookiePath(dbPath, m.legacyFileName)
	if err != nil {
		return snapshot, nil
	}
	snapshot.legacyPath = legacyPath
	legacyValue, readErr := os.ReadFile(legacyPath)
	if readErr == nil {
		snapshot.legacyValue = legacyValue
		snapshot.hadLegacyValue = true
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("tracker auth: snapshot %s legacy auth key: %w", m.trackerID, readErr)
	}
	return snapshot, nil
}

func (m keyedFileStateManager) Delete(ctx context.Context, dbPath string) error {
	var errs []error
	if err := DeleteAuthState(ctx, dbPath, m.trackerID, m.stateKey); err != nil && encryptedAuthStateMayExist(dbPath) {
		errs = append(errs, fmt.Errorf("tracker auth: delete %s auth state: %w", m.trackerID, err))
	} else if err != nil && !errors.Is(err, ErrAuthStateNotFound) {
		errs = append(errs, fmt.Errorf("tracker auth: delete %s auth state uncertain: %w", m.trackerID, err))
	}
	if m.legacyFileName == "" {
		return errors.Join(errs...)
	}
	legacyPath, err := servicedb.CookiePath(dbPath, m.legacyFileName)
	if err != nil {
		errs = append(errs, fmt.Errorf("tracker auth: resolve %s legacy auth key path: %w", m.trackerID, err))
	} else if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("tracker auth: delete %s legacy auth key: %w", m.trackerID, err))
	}
	return errors.Join(errs...)
}

func (s *keyedFileStateSnapshot) Restore(ctx context.Context) error {
	if s == nil {
		return nil
	}
	rollbackCtx := contextWithoutCancel(ctx)
	var errs []error
	if s.hadState {
		if err := SaveAuthState(rollbackCtx, s.dbPath, s.manager.trackerID, s.manager.stateKey, s.stateValue); err != nil {
			errs = append(errs, fmt.Errorf("tracker auth: restore %s auth state: %w", s.manager.trackerID, err))
		}
	}
	if s.hadLegacyValue && s.legacyPath != "" {
		if err := os.MkdirAll(filepath.Dir(s.legacyPath), 0o700); err != nil {
			errs = append(errs, fmt.Errorf("tracker auth: restore %s legacy auth key dir: %w", s.manager.trackerID, err))
		} else if err := os.WriteFile(s.legacyPath, s.legacyValue, 0o600); err != nil {
			errs = append(errs, fmt.Errorf("tracker auth: restore %s legacy auth key: %w", s.manager.trackerID, err))
		}
	}
	return errors.Join(errs...)
}

func encryptedAuthStateMayExist(dbPath string) bool {
	if strings.TrimSpace(dbPath) == "" {
		return false
	}
	_, err := authmaterial.LoadFromDBPath(dbPath)
	return err == nil
}
