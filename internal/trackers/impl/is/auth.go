// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import "github.com/autobrr/upbrr/pkg/api"

func (Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          "IS",
		DisplayName:        "IS",
		AuthKind:           "cookies",
		SupportsCookieFile: true,
	}
}
