// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
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
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
}

func TestLoadTrackerCookieMapStoredValueOverridesStartupCookie(t *testing.T) {
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
	if values["session"] != "from-db" {
		t.Fatal("expected db cookie to override startup value")
	}
	if values["persisted"] != "keep-me" {
		t.Fatal("expected db-only cookie to remain available")
	}
	if values["fresh"] != "from-file" {
		t.Fatal("expected startup-only cookie to be loaded")
	}
}

func TestCookieMapToHTTPCookiesPreservesCookieValueWhitespace(t *testing.T) {
	t.Parallel()

	paddedName := " session "
	got := CookieMapToHTTPCookies(map[string]string{
		paddedName: " padded ",
		"empty":    "",
	}, " .example.org ")
	if len(got) != 1 {
		t.Fatalf("expected one HTTP cookie, got count=%d", len(got))
	}
	if got[0].Name != "session" || got[0].Value != " padded " {
		t.Fatal("expected cookie value whitespace to be preserved")
	}
	if got[0].Domain != ".example.org" {
		t.Fatal("expected trimmed domain")
	}
}

func TestLoadTrackerHTTPCookiesStoredValueOverridesStartupCookie(t *testing.T) {
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
	if values["session"] != "from-db" {
		t.Fatal("expected db cookie to override startup value")
	}
	if values["persisted"] != "keep-me" {
		t.Fatal("expected db-only cookie to remain available")
	}
	if values["fresh"] != "from-file" {
		t.Fatal("expected startup-only cookie to be loaded")
	}
	for _, cookie := range loaded {
		if cookie == nil {
			continue
		}
		if cookie.Domain != "bj-share.info" {
			t.Fatal("expected domain to be applied")
		}
	}
}

func TestLoadTrackerCookieMapMalformedLegacyJSONSurfacesParseError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	jsonPath := legacyBTNCookiePathByExt(t, dbPath, ".json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":`), 0o600); err != nil {
		t.Fatalf("write malformed cookie file: %v", err)
	}

	_, err := LoadTrackerCookieMap(ctx, dbPath, "BTN")
	if err == nil {
		t.Fatal("expected malformed cookie file error")
	}
	if errors.Is(err, ErrTrackerCookiesNotFound) {
		t.Fatalf("expected parse error, got not-found: %v", err)
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("expected JSON parse context, got %v", err)
	}
}

func TestLoadTrackerHTTPCookiesMalformedLegacyJSONSurfacesParseError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	jsonPath := legacyBTNCookiePathByExt(t, dbPath, ".json")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":`), 0o600); err != nil {
		t.Fatalf("write malformed cookie file: %v", err)
	}

	_, err := LoadTrackerHTTPCookies(ctx, dbPath, "BTN", "backup.landof.tv")
	if err == nil {
		t.Fatal("expected malformed cookie file error")
	}
	if errors.Is(err, ErrTrackerCookiesNotFound) {
		t.Fatalf("expected parse error, got not-found: %v", err)
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("expected JSON parse context, got %v", err)
	}
}

func TestLoadTrackerCookieMapIgnoresLegacyNetscapeNoValidCookies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "comment-only", content: "# Netscape HTTP Cookie File\n# no cookies\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "upbrr.db")
			txtPath := legacyBTNCookiePathByExt(t, dbPath, ".txt")
			if err := os.MkdirAll(filepath.Dir(txtPath), 0o755); err != nil {
				t.Fatalf("mkdir cookie dir: %v", err)
			}
			if err := os.WriteFile(txtPath, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write legacy txt cookie file: %v", err)
			}

			_, err := LoadTrackerCookieMap(ctx, dbPath, "BTN")
			if !errors.Is(err, ErrTrackerCookiesNotFound) {
				t.Fatalf("expected not-found for no valid Netscape cookies, got %v", err)
			}
		})
	}
}

func TestLoadTrackerHTTPCookiesIgnoresLegacyNetscapeDomainMismatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	txtPath := legacyBTNCookiePathByExt(t, dbPath, ".txt")
	if err := os.MkdirAll(filepath.Dir(txtPath), 0o755); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	content := ".example.org\tTRUE\t/\tTRUE\t0\tsession\tfrom-file\n"
	if err := os.WriteFile(txtPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write legacy txt cookie file: %v", err)
	}

	_, err := LoadTrackerHTTPCookies(ctx, dbPath, "BTN", "backup.landof.tv")
	if !errors.Is(err, ErrTrackerCookiesNotFound) {
		t.Fatalf("expected not-found for domain-mismatched Netscape cookies, got %v", err)
	}
}

func TestDeleteTrackerCookiesRestoresDBCookiesWhenLegacyDeleteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)
	if err := SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "from-db"}); err != nil {
		t.Fatalf("seed db cookies: %v", err)
	}

	candidates := commonhttp.CookiePathCandidates(dbPath, "AR", ".txt")
	if len(candidates) != 1 {
		t.Fatalf("expected one legacy cookie path, got %#v", candidates)
	}
	if err := os.MkdirAll(filepath.Dir(candidates[0]), 0o700); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.Mkdir(candidates[0], 0o700); err != nil {
		t.Fatalf("create blocking cookie dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(candidates[0], "child"), []byte("blocks remove"), 0o600); err != nil {
		t.Fatalf("write blocking cookie child: %v", err)
	}

	err := DeleteTrackerCookies(ctx, dbPath, "AR")
	if err == nil {
		t.Fatal("expected legacy cookie delete failure")
	}
	if !strings.Contains(err.Error(), "legacy cookie file") {
		t.Fatalf("expected legacy cookie delete error, got %v", err)
	}

	values, err := LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("load restored cookies: %v", err)
	}
	if values["session"] != "from-db" {
		t.Fatal("expected DB cookie restore after legacy failure")
	}
}

func TestDeleteTrackerCookiesRestoresRemovedLegacyCandidateWhenLaterLegacyDeleteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)
	if err := SaveTrackerCookieMap(ctx, dbPath, "AR", map[string]string{"session": "from-db"}); err != nil {
		t.Fatalf("seed db cookies: %v", err)
	}

	candidates := commonhttp.CookiePathCandidates(dbPath, "AR", ".txt", ".json")
	if len(candidates) != 2 {
		t.Fatalf("expected txt/json cookie paths, got %#v", candidates)
	}
	txtPath := candidates[0]
	jsonPath := candidates[1]
	txtContent := []byte(".alpharatio.cc\tTRUE\t/\tTRUE\t0\tlegacy\tfrom-file\n")
	if err := os.MkdirAll(filepath.Dir(txtPath), 0o700); err != nil {
		t.Fatalf("mkdir cookie dir: %v", err)
	}
	if err := os.WriteFile(txtPath, txtContent, 0o600); err != nil {
		t.Fatalf("write txt legacy cookie: %v", err)
	}
	if err := os.Mkdir(jsonPath, 0o700); err != nil {
		t.Fatalf("create blocking json cookie dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jsonPath, "child"), []byte("blocks remove"), 0o600); err != nil {
		t.Fatalf("write blocking json child: %v", err)
	}

	err := DeleteTrackerCookies(ctx, dbPath, "AR")
	if err == nil {
		t.Fatal("expected json legacy cookie delete failure")
	}
	if !strings.Contains(err.Error(), "legacy cookie file") {
		t.Fatalf("expected legacy cookie delete error, got %v", err)
	}

	restoredTxt, err := os.ReadFile(txtPath)
	if err != nil {
		t.Fatalf("expected txt legacy cookie to be restored: %v", err)
	}
	if string(restoredTxt) != string(txtContent) {
		t.Fatalf("unexpected restored txt cookie content: %q", restoredTxt)
	}
	if _, err := os.Stat(filepath.Join(jsonPath, "child")); err != nil {
		t.Fatalf("expected blocking json directory to remain: %v", err)
	}
	values, err := LoadTrackerCookieMap(ctx, dbPath, "AR")
	if err != nil {
		t.Fatalf("load restored cookies: %v", err)
	}
	if values["session"] != "from-db" {
		t.Fatal("expected DB cookie restore after legacy failure")
	}
}

func TestDeleteTrackerCookiesDBDeleteErrorPreservesLegacyFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)

	if err := SaveTrackerCookieMap(ctx, dbPath, "BLU", map[string]string{"session": "from-db"}); err != nil {
		t.Fatalf("seed db cookies: %v", err)
	}
	legacyPaths := writeLegacyCookieCandidates(t, dbPath, "BLU")
	rejectTrackerCookieDeletes(ctx, t, dbPath)

	err := DeleteTrackerCookies(ctx, dbPath, "BLU")
	if err == nil {
		t.Fatal("expected db delete error")
	}
	if !strings.Contains(err.Error(), "delete tracker BLU from db") {
		t.Fatalf("expected wrapped db delete error, got %v", err)
	}
	for _, path := range legacyPaths {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected legacy file to remain after db delete failure: %v", statErr)
		}
	}
}

func TestDeleteTrackerCookiesSuccessRemovesLegacyCandidatesOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := writeWebAuthFile(t, "tester", "password-hash")
	initTestCookieDBSchema(t, dbPath)

	if err := SaveTrackerCookieMap(ctx, dbPath, "BLU", map[string]string{"session": "from-db"}); err != nil {
		t.Fatalf("seed db cookies: %v", err)
	}
	legacyPaths := writeLegacyCookieCandidates(t, dbPath, "BLU")
	untouchedPath := filepath.Join(filepath.Dir(legacyPaths[0]), "BLU.bak")
	if err := os.WriteFile(untouchedPath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unrelated legacy file: %v", err)
	}

	if err := DeleteTrackerCookies(ctx, dbPath, "BLU"); err != nil {
		t.Fatalf("DeleteTrackerCookies: %v", err)
	}
	for _, path := range legacyPaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected legacy candidate %s to be removed, got %v", path, err)
		}
	}
	if _, err := os.Stat(untouchedPath); err != nil {
		t.Fatalf("expected non-candidate legacy file to remain: %v", err)
	}
	if got := countTrackerCookies(ctx, t, dbPath, "BLU"); got != 0 {
		t.Fatalf("expected db cookies to be removed, got %d", got)
	}
}

func TestDeleteTrackerCookiesHelperUnavailableRemovesLegacyFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	legacyPaths := writeLegacyCookieCandidates(t, dbPath, "BTN")

	if err := DeleteTrackerCookies(ctx, dbPath, "BTN"); err != nil {
		t.Fatalf("DeleteTrackerCookies: %v", err)
	}
	for _, path := range legacyPaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected legacy file %s to be removed with unavailable helper, got %v", path, err)
		}
	}
}

func TestDeleteTrackerCookiesMissingLegacyFilesIgnored(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	if err := DeleteTrackerCookies(ctx, dbPath, "BTN"); err != nil {
		t.Fatalf("DeleteTrackerCookies: %v", err)
	}
}

func writeLegacyCookieCandidates(t *testing.T, dbPath string, trackerID string) []string {
	t.Helper()

	candidates := commonhttp.CookiePathCandidates(dbPath, trackerID, ".txt", ".json")
	if len(candidates) != 2 {
		t.Fatalf("expected txt/json cookie paths, got %#v", candidates)
	}
	for _, candidate := range candidates {
		if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
			t.Fatalf("mkdir cookie dir: %v", err)
		}
		if err := os.WriteFile(candidate, []byte("cookie"), 0o600); err != nil {
			t.Fatalf("write legacy cookie candidate: %v", err)
		}
	}
	return candidates
}

// legacyBTNCookiePathByExt selects the concrete legacy candidate path for tests
// that need to seed one cookie format without depending on candidate ordering.
func legacyBTNCookiePathByExt(t *testing.T, dbPath string, ext string) string {
	t.Helper()

	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, "BTN", ".txt", ".json") {
		if filepath.Ext(candidate) == ext {
			return candidate
		}
	}
	t.Fatalf("expected %s legacy cookie path", ext)
	return ""
}

func rejectTrackerCookieDeletes(ctx context.Context, t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_tracker_cookie_delete BEFORE DELETE ON tracker_cookies
		BEGIN
			SELECT RAISE(ABORT, 'blocked cookie delete');
		END`); err != nil {
		t.Fatalf("create cookie delete rejection trigger: %v", err)
	}
}

func countTrackerCookies(ctx context.Context, t *testing.T, dbPath string, trackerID string) int {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracker_cookies WHERE tracker_id = ?`, trackerID).Scan(&count); err != nil {
		t.Fatalf("count tracker cookies: %v", err)
	}
	return count
}
