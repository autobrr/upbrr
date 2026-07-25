// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
)

// DataLookupConfigured reports whether HDB metadata lookup credentials are available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	for name, entry := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(name), "HDB") {
			return strings.TrimSpace(entry.Username) != "" && strings.TrimSpace(entry.Passkey) != ""
		}
	}
	return false
}
