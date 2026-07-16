// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d *Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return func(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
		return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, login, d.baseURL)
	}
}

func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          "BTN",
		DisplayName:        "BTN",
		AuthKind:           "api_key_cookies_login_manual_2fa",
		SupportsCookieFile: true,
		SupportsLogin:      true,
		SupportsAutoLogin:  true,
		SupportsTOTP:       true,
		SupportsManual2FA:  true,
		RequiresAPIKey:     true,
		Notes:              []string{"API key is required for torrent resolution; cookies/login cover upload auth."},
	}
}

// AuthPolicy keeps BTN's API/search prerequisite separate from upload-session readiness.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	// User-facing prerequisite messages contain no credential values.
	//nolint:gosec
	return &trackers.AuthPolicy{
		ResolveAPIKey: func(cfg config.Config, _ config.TrackerConfig) string {
			return config.ResolveBTNAPIToken(cfg)
		},
		APIKeyRequiresUploadSession: true,
		MissingAPIKeyMessage:        "API key is required for torrent resolution; imported cookies or login credentials cover upload auth",
		UploadSessionMissingMessage: "API key covers torrent resolution; imported cookies or login credentials required for upload auth",
	}
}
