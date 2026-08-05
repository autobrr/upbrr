// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
)

// writeTestAuthFile avoids production-cost password hashing in tests that only
// need usable persisted auth material. Password/KDF behavior stays covered by
// the dedicated authmaterial and web-auth tests.
func writeTestAuthFile(t *testing.T, dbPath string, username string, allowUnencryptedExport bool) {
	t.Helper()

	authfixture.Write(t, dbPath, authfixture.Options{
		Username:               username,
		AllowUnencryptedExport: allowUnencryptedExport,
	})
}
