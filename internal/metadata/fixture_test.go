// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"testing"
)

// newTestService isolates database-adjacent files even when persistence is faked.
// Explicit config paths and path options retain their caller-owned authority.
func newTestService(t *testing.T, repo repository, opts ...Option) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "metadata.sqlite")
	options := append([]Option{WithTagsPathFromDB(dbPath), WithSRRDBPaths(dbPath)}, opts...)
	options = append(options, func(service *Service) {
		if service.cfg.MainSettings.DBPath == "" {
			service.cfg.MainSettings.DBPath = dbPath
		}
	})
	return NewService(repo, options...)
}
