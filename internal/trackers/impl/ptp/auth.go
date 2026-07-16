// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

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
		TrackerID:          "PTP",
		DisplayName:        "PTP",
		AuthKind:           "cookies_login_manual_2fa",
		SupportsCookieFile: true,
		SupportsLogin:      true,
		SupportsAutoLogin:  true,
		SupportsTOTP:       true,
		SupportsManual2FA:  true,
	}
}

// AuthPolicy records PTP's announce-URL prerequisite for credential login.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	return &trackers.AuthPolicy{LoginRequiresAnnounceURL: true}
}
