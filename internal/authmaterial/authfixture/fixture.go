// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package authfixture writes web-auth material for tests that exercise
// encrypted persistence without exercising password hashing.
package authfixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

const (
	defaultUsername          = "tester"
	defaultEncryptionKeySeed = "stable-encryption-seed-for-tests"
	testPasswordHash         = "precomputed-test-password-hash"
)

// Options customizes the non-password fields used by Write.
type Options struct {
	Username               string
	EncryptionKeySeed      string
	AllowUnencryptedExport bool
}

// Write creates usable persisted auth material without running Argon2.
//
// The password hash is intentionally synthetic and cannot authenticate a
// login. Tests of password hashing or login verification must use the real
// authmaterial APIs.
func Write(t testing.TB, dbPath string, options ...Options) {
	t.Helper()

	if len(options) > 1 {
		t.Fatal("auth fixture accepts at most one options value")
	}

	option := Options{}
	if len(options) == 1 {
		option = options[0]
	}
	username := strings.TrimSpace(option.Username)
	if username == "" {
		username = defaultUsername
	}
	seed := strings.TrimSpace(option.EncryptionKeySeed)
	if seed == "" {
		seed = defaultEncryptionKeySeed
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create auth fixture dir: %v", err)
	}
	payload, err := json.Marshal(authmaterial.Record{
		Username:               username,
		PasswordHash:           testPasswordHash,
		EncryptionKeySeed:      seed,
		AllowUnencryptedExport: option.AllowUnencryptedExport,
	})
	if err != nil {
		t.Fatalf("marshal auth fixture: %v", err)
	}
	if err := os.WriteFile(authmaterial.AuthFilePath(dbPath), payload, 0o600); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}
}
