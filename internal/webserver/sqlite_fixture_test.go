// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"os"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
)

func ensureMigratedTestDB(t testing.TB, dbPath string) {
	t.Helper()

	if _, err := os.Stat(dbPath); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspect migrated database fixture: %v", err)
	}
	dbfixture.WriteMigrated(t, dbPath)
}
