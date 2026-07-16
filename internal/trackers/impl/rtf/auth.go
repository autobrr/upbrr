// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return func(ctx context.Context, cfg config.TrackerConfig, dbPath string, request api.TrackerAuthLoginRequest) error {
		return resolveSessionForTrackerAuthLoginAt(ctx, cfg, dbPath, request, d.baseURL)
	}
}

func (d Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:         "RTF",
		DisplayName:       "RTF",
		AuthKind:          "api_key_credential_refresh",
		SupportsLogin:     true,
		SupportsAutoLogin: true,
		RequiresAPIKey:    true,
	}
}
