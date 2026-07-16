// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return cookieLoginCapability("AR")
}

// AuthSessionResolver exposes AR's upload session engine to generic auth
// orchestration. The engine validates stored cookies, performs credential login
// when required, and persists only a proven session.
func (d *Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return func(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
		_, _, err := resolveSession(ctx, cfg, dbPath, nil)
		return err
	}
}

func cookieLoginCapability(name string) api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          name,
		DisplayName:        name,
		AuthKind:           "cookies_login",
		SupportsCookieFile: true,
		SupportsLogin:      true,
		SupportsAutoLogin:  true,
	}
}
