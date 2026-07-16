// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import "github.com/autobrr/upbrr/pkg/api"

func (Definition) AuthCapability() api.TrackerAuthCapability { return bjsCookieAuth("BJS") }
func bjsCookieAuth(name string) api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          name,
		DisplayName:        name,
		AuthKind:           "cookies",
		SupportsCookieFile: true,
	}
}
