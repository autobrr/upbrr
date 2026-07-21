// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import "github.com/autobrr/upbrr/pkg/api"

// RepositoryCapabilities returns immutable borrowed capability views over this
// SQLite owner. Every view shares its connection, transaction runner, retry
// policy, migration state, and lifecycle.
func (r *SQLiteRepository) RepositoryCapabilities() api.RepositoryCapabilities {
	return api.RepositoryCapabilitiesFrom(r)
}
