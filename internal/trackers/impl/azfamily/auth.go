// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import "github.com/autobrr/upbrr/pkg/api"

func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          d.Name(),
		DisplayName:        d.Name(),
		AuthKind:           "cookies",
		SupportsCookieFile: true,
	}
}
