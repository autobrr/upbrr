// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
)

func generateStableEncryptionSeed() (string, error) {
	return authmaterial.GenerateSeed()
}

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
		if err := cookies.RewrapCookiesWithAuthChange(ctx, s.backend.repo.RawDB(), oldMaterial, newMaterial); err != nil {
			return err
		}
		if err := s.auth.AdvancePendingUpgrade(oldRecord.Username, authmaterial.UpgradeStageCookiesRewrapped); err != nil {
			return fmt.Errorf("auth rewrap: persist cookie phase: %w", err)
		}
		pending.Stage = authmaterial.UpgradeStageCookiesRewrapped
	}

	if pending.Stage == authmaterial.UpgradeStageCookiesRewrapped {
		sourceMaterials := []authmaterial.Material{oldMaterial, newMaterial}
		if err := config.RewrapSecretsInDatabaseWithFallback(ctx, s.backend.repo, sourceMaterials, newMaterial); err != nil {
			return err
		}
		if err := s.auth.AdvancePendingUpgrade(oldRecord.Username, authmaterial.UpgradeStageDataRewrapped); err != nil {
			return fmt.Errorf("auth rewrap: persist data phase: %w", err)
		}
		pending.Stage = authmaterial.UpgradeStageDataRewrapped
	}

	return nil
}
