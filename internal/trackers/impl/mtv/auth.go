// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func (d *Definition) AuthSessionResolver() trackers.AuthSessionResolver {
	return ResolveSessionForTrackerAuthLogin
}

func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          "MTV",
		DisplayName:        "MTV",
		AuthKind:           "api_key_cookies_login_manual_2fa",
		SupportsCookieFile: true,
		SupportsLogin:      true,
		SupportsAutoLogin:  true,
		SupportsTOTP:       true,
		SupportsManual2FA:  true,
		RequiresAPIKey:     true,
		Notes:              []string{"API key covers Torznab/search; cookies/login cover upload authkey."},
	}
}
