// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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

	oldMaterial := oldRecord.authMaterial()
	newMaterial := newRecord.authMaterial()

	if err := cookies.RewrapCookiesWithAuthChange(ctx, s.backend.repo.RawDB(), oldMaterial, newMaterial); err != nil {
		return err
	}
	if err := config.RewrapSecretsInDatabase(ctx, s.backend.repo, oldMaterial, newMaterial); err != nil {
		// Operators: cookies and config secret rewrap are not currently bound to one shared DB transaction.
		rollbackErr := cookies.RewrapCookiesWithAuthChange(ctx, s.backend.repo.RawDB(), newMaterial, oldMaterial)
		if s.backend.logger != nil {
			if rollbackErr != nil {
				s.backend.logger.Errorf("web: protected data rewrap partial rollback failed phase=upgrade config_error=%v rollback_error=%v", err, rollbackErr)
			} else {
				s.backend.logger.Warnf("web: protected data rewrap used compensating rollback phase=upgrade config_error=%v", err)
			}
		}
		if rollbackErr != nil {
			return fmt.Errorf("protected data rewrap upgrade failed: %w", errors.Join(err, rollbackErr))
		}
		return err
	}

	return nil
}

func (s *Server) rollbackProtectedDataForAuthChange(r *http.Request, currentRecord, previousRecord authRecord) error {
	if s == nil || s.backend == nil || s.backend.repo == nil {
		return nil
	}

	ctx := r.Context()
	currentMaterial := currentRecord.authMaterial()
	previousMaterial := previousRecord.authMaterial()

	if err := cookies.RewrapCookiesWithAuthChange(ctx, s.backend.repo.RawDB(), currentMaterial, previousMaterial); err != nil {
		return err
	}
	if err := config.RewrapSecretsInDatabase(ctx, s.backend.repo, currentMaterial, previousMaterial); err != nil {
		// Operators: cookies and config secret rewrap are not currently bound to one shared DB transaction.
		rollbackErr := cookies.RewrapCookiesWithAuthChange(ctx, s.backend.repo.RawDB(), previousMaterial, currentMaterial)
		if s.backend.logger != nil {
			if rollbackErr != nil {
				s.backend.logger.Errorf("web: protected data rewrap partial rollback failed phase=auth-record-rollback config_error=%v rollback_error=%v", err, rollbackErr)
			} else {
				s.backend.logger.Warnf("web: protected data rewrap used compensating rollback phase=auth-record-rollback config_error=%v", err)
			}
		}
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("protected data rewrap rollback failed: %w", err),
				fmt.Errorf("cookie rollback failed: %w", rollbackErr),
			)
		}
		return err
	}

	return nil
}
