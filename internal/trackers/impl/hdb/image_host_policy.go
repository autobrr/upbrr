// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import "github.com/autobrr/upbrr/internal/trackers"

func (d *Definition) ImageHostPolicy() *trackers.ImageHostPolicy {
	return &trackers.ImageHostPolicy{AllowedHosts: []string{"hdb"}, DisableWithoutRehost: true}
}
