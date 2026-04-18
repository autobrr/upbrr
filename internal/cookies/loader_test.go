// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
)

func TestIsMissingCookieSchemaError(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	ctx := context.Background()

	var missingTableErr error
	if _, err := db.ExecContext(ctx, `SELECT * FROM missing_cookie_table`); err != nil {
		missingTableErr = err
	} else {
		t.Fatal("expected missing table error")
	}

	if !isMissingCookieSchemaError(missingTableErr) {
		t.Fatalf("expected missing table error to be classified as missing schema: %v", missingTableErr)
	}

	var genericSQLiteErr error
	if _, err := db.ExecContext(ctx, `SELECT FROM tracker_cookies`); err != nil {
		genericSQLiteErr = err
	} else {
		t.Fatal("expected generic sqlite error")
	}

	if strings.Contains(strings.ToLower(genericSQLiteErr.Error()), "no such table") {
		t.Fatalf("expected non-schema sqlite error, got %v", genericSQLiteErr)
	}

	if isMissingCookieSchemaError(genericSQLiteErr) {
		t.Fatalf("expected generic sqlite error to not be classified as missing schema: %v", genericSQLiteErr)
	}
}

func initTestCookieDBSchema(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	statements := []string{
		`CREATE TABLE config_settings (
			section TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			updated_at DATETIME
		)`,
		`CREATE TABLE tracker_cookies (
			tracker_id TEXT NOT NULL,
			cookie_name TEXT NOT NULL,
			encrypted_value TEXT NOT NULL,
			nonce TEXT NOT NULL,
			auth_tag TEXT NOT NULL,
			created_at DATETIME,
			updated_at DATETIME,
			PRIMARY KEY (tracker_id, cookie_name)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
}

func TestLoadTrackerCookieMapStartupCookieOverridesStoredValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)

	if err := SaveTrackerCookieMap(ctx, dbPath, "BLU", map[string]string{
		"session":   "from-db",
		"persisted": "keep-me",
	}); err != nil {
		t.Fatalf("seed db cookies: %v", err)
	}

	candidates := commonhttp.CookiePathCandidates(dbPath, "BLU", ".txt", ".json")
	jsonPath := ""
	for _, candidate := range candidates {
		if filepath.Ext(candidate) == ".json" {
			jsonPath = candidate
			break
		}
	}
	if jsonPath == "" {
		t.Fatalf("expected json cookie path, got %#v", candidates)
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":"from-startup","fresh":"from-file"}`), 0o600); err != nil {
		t.Fatalf("write startup cookie file: %v", err)
	}

	values, err := LoadTrackerCookieMap(ctx, dbPath, "BLU")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if values["session"] != "from-startup" {
		t.Fatalf("expected startup cookie to override db value, got %#v", values)
	}
	if values["persisted"] != "keep-me" {
		t.Fatalf("expected db-only cookie to remain available, got %#v", values)
	}
	if values["fresh"] != "from-file" {
		t.Fatalf("expected startup-only cookie to be loaded, got %#v", values)
	}
}

func TestLoadTrackerHTTPCookiesStartupCookieOverridesStoredValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)

	if err := SaveTrackerCookieMap(ctx, dbPath, "BJS", map[string]string{
		"session":   "from-db",
		"persisted": "keep-me",
	}); err != nil {
		t.Fatalf("seed db cookies: %v", err)
	}

	candidates := commonhttp.CookiePathCandidates(dbPath, "BJS", ".txt", ".json")
	jsonPath := ""
	for _, candidate := range candidates {
		if filepath.Ext(candidate) == ".json" {
			jsonPath = candidate
			break
		}
	}
	if jsonPath == "" {
		t.Fatalf("expected json cookie path, got %#v", candidates)
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":"from-startup","fresh":"from-file"}`), 0o600); err != nil {
		t.Fatalf("write startup cookie file: %v", err)
	}

	loaded, err := LoadTrackerHTTPCookies(ctx, dbPath, "BJS", "bj-share.info")
	if err != nil {
		t.Fatalf("LoadTrackerHTTPCookies: %v", err)
	}

	values := httpCookiesToMap(loaded)
	if values["session"] != "from-startup" {
		t.Fatalf("expected startup cookie to override db value, got %#v", values)
	}
	if values["persisted"] != "keep-me" {
		t.Fatalf("expected db-only cookie to remain available, got %#v", values)
	}
	if values["fresh"] != "from-file" {
		t.Fatalf("expected startup-only cookie to be loaded, got %#v", values)
	}
	for _, cookie := range loaded {
		if cookie == nil {
			continue
		}
		if cookie.Domain != "bj-share.info" {
			t.Fatalf("expected domain to be applied, got cookie %#v", cookie)
		}
	}
}
