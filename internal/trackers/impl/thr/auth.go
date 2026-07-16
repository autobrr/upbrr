// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"context"
	"errors"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:         "THR",
		DisplayName:       "THR",
		AuthKind:          "credential_login",
		SupportsLogin:     true,
		SupportsAutoLogin: true,
	}
}

func (Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return resolveAuthSession
}

func resolveAuthSession(ctx context.Context, cfg config.TrackerConfig, _ string, _ api.TrackerAuthLoginRequest) error {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return errors.New("trackers: THR username/password not configured")
	}
	if _, err := LoginSession(ctx, cfg); err != nil {
		if errors.Is(err, ErrLoginFailed) {
			return &trackers.AuthResolutionError{
				Reason:           "login failed",
				ConfirmedInvalid: true,
				Err:              err,
			}
		}
		return &trackers.AuthResolutionError{
			Reason:    "remote login unavailable",
			Transient: true,
			Err:       err,
		}
	}
	return nil
}
