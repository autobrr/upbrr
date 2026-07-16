// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import "github.com/autobrr/upbrr/pkg/api"

func (d *Definition) AuthCapability() api.TrackerAuthCapability { return cookieCapability("ASC") }

func cookieCapability(name string) api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          name,
		DisplayName:        name,
		AuthKind:           "cookies",
		SupportsCookieFile: true,
	}
}
