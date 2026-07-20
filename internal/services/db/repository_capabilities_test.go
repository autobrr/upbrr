// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestRepositoryCapabilitiesShareSQLiteOwner(t *testing.T) {
	t.Parallel()

	repo, err := Open(filepath.Join(t.TempDir(), "capabilities.sqlite"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	capabilities, err := api.NewRepositoryCapabilities(repo)
	if err != nil {
		t.Fatalf("new repository capabilities: %v", err)
	}
	if capabilities.ReleaseState() != repo || capabilities.Selections() != repo || capabilities.History() != repo ||
		capabilities.Uploads() != repo || capabilities.Trackers() != repo || capabilities.Media() != repo {
		t.Fatal("repository capabilities do not share one SQLite owner")
	}
}
