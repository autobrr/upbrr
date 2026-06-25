// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubCookieStore struct {
	cookies map[string]string
	err     error
}

func (s stubCookieStore) GetAllTrackerCookies(context.Context, string, []byte) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.cookies, nil
}

func TestLoadCookiesForTrackerUsesCookieStoreWhenNoStartupCookieExists(t *testing.T) {
	t.Parallel()

	got, err := LoadCookiesForTracker(
		context.Background(),
		filepath.Join(t.TempDir(), "upbrr.db"),
		"blu",
		stubCookieStore{cookies: map[string]string{"session": "from-db"}},
		[]byte("01234567890123456789012345678901"),
	)
	if err != nil {
		t.Fatalf("LoadCookiesForTracker: %v", err)
	}
	if got["session"] != "from-db" {
		t.Fatalf("expected cookie from store, got %#v", got)
	}
}

func TestLoadCookiesForTrackerCookieStoreOverridesStartupCookie(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "state", "upbrr.db")
	candidates := CookiePathCandidates(dbPath, "blu", ".txt", ".json")

	jsonPath := ""
	for _, candidate := range candidates {
		if strings.HasSuffix(candidate, ".json") {
			jsonPath = candidate
			break
		}
	}
	if jsonPath == "" {
		t.Fatalf("expected json cookie candidate, got %#v", candidates)
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":"from-startup","extra":"from-file"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadCookiesForTracker(
		context.Background(),
		dbPath,
		"blu",
		stubCookieStore{cookies: map[string]string{"session": "from-db", "persisted": "keep-me"}},
		[]byte("01234567890123456789012345678901"),
	)
	if err != nil {
		t.Fatalf("LoadCookiesForTracker: %v", err)
	}
	if got["session"] != "from-db" {
		t.Fatalf("expected store cookie to override startup value, got %#v", got)
	}
	if got["persisted"] != "keep-me" {
		t.Fatalf("expected db-only cookie to be preserved, got %#v", got)
	}
	if got["extra"] != "from-file" {
		t.Fatalf("expected startup-only cookie to be returned, got %#v", got)
	}
}

func TestLoadCookiesForTrackerFallsBackToJSONFileWhenStoreHasNoCookies(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "state", "upbrr.db")
	candidates := CookiePathCandidates(dbPath, "blu", ".txt", ".json")
	if len(candidates) != 2 {
		t.Fatalf("expected txt and json cookie candidates, got %#v", candidates)
	}

	jsonPath := ""
	for _, candidate := range candidates {
		if strings.HasSuffix(candidate, ".json") {
			jsonPath = candidate
			break
		}
	}
	if jsonPath == "" {
		t.Fatalf("expected json cookie candidate, got %#v", candidates)
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"session":"from-json"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadCookiesForTracker(
		context.Background(),
		dbPath,
		"blu",
		stubCookieStore{cookies: map[string]string{}},
		[]byte("01234567890123456789012345678901"),
	)
	if err != nil {
		t.Fatalf("LoadCookiesForTracker: %v", err)
	}
	if got["session"] != "from-json" {
		t.Fatalf("expected JSON fallback cookie, got %#v", got)
	}
}

func TestLoadCookiesForTrackerFallsBackToNetscapeFileWithoutDomainFilter(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "state", "upbrr.db")
	candidates := CookiePathCandidates(dbPath, "blu", ".txt", ".json")
	if len(candidates) != 2 {
		t.Fatalf("expected txt and json cookie candidates, got %#v", candidates)
	}

	txtPath := ""
	for _, candidate := range candidates {
		if strings.HasSuffix(candidate, ".txt") {
			txtPath = candidate
			break
		}
	}
	if txtPath == "" {
		t.Fatalf("expected txt cookie candidate, got %#v", candidates)
	}
	if err := os.MkdirAll(filepath.Dir(txtPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	netscape := ".example.org\tTRUE\t/\tFALSE\t0\tsession\t from-txt \n"
	if err := os.WriteFile(txtPath, []byte(netscape), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadCookiesForTracker(
		context.Background(),
		dbPath,
		"blu",
		stubCookieStore{cookies: map[string]string{}},
		[]byte("01234567890123456789012345678901"),
	)
	if err != nil {
		t.Fatalf("LoadCookiesForTracker: %v", err)
	}
	if got["session"] != " from-txt " {
		t.Fatalf("expected Netscape fallback cookie, got %#v", got)
	}
}

func TestLoadNetscapeCookiesPreservesValueWhitespaceAndSkipsEmptyNameOrValue(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cookies.txt")
	content := strings.Join([]string{
		".example.org\tTRUE\t/\tFALSE\t0\t\tmissing-name",
		".example.org\tTRUE\t/\tFALSE\t0\tempty\t",
		".example.org\tTRUE\t/\tFALSE\t0\tsession\t padded ",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := LoadNetscapeCookies(path, "example.org")
	if err != nil {
		t.Fatalf("LoadNetscapeCookies: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one valid cookie, got %#v", got)
	}
	if got[0].Name != "session" || got[0].Value != " padded " {
		t.Fatalf("expected padded Netscape value to be preserved, got %#v", got[0])
	}
}

func TestLoadCookiesForTrackerReturnsStoreError(t *testing.T) {
	t.Parallel()

	_, err := LoadCookiesForTracker(
		context.Background(),
		filepath.Join(t.TempDir(), "state", "upbrr.db"),
		"blu",
		stubCookieStore{err: errors.New("database unavailable")},
		[]byte("01234567890123456789012345678901"),
	)
	if err == nil {
		t.Fatal("expected cookie store error to be returned")
	}
	if !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("expected wrapped cookie store error, got %v", err)
	}
}

func TestLoadCookiesForTrackerReturnsErrorWhenNoSourcesExist(t *testing.T) {
	t.Parallel()

	_, err := LoadCookiesForTracker(context.Background(), filepath.Join(t.TempDir(), "upbrr.db"), "blu", nil, nil)
	if err == nil {
		t.Fatal("expected missing cookie sources to fail")
	}
}

func TestExtractHTTPErrorDetailPrefersErrorsMap(t *testing.T) {
	t.Parallel()

	body := []byte(`{"message":"Validation failed","errors":{"name":["The name has already been taken."],"tmdb":["The tmdb field is required."]},"api_token":"secret"}`)
	got := ExtractHTTPErrorDetail(body)

	if !strings.Contains(got, "name: The name has already been taken.") {
		t.Fatalf("expected name error, got %q", got)
	}
	if !strings.Contains(got, "tmdb: The tmdb field is required.") {
		t.Fatalf("expected tmdb error, got %q", got)
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("expected sensitive value redacted, got %q", got)
	}
}

func TestExtractHTTPErrorDetailSkipsBooleanStatusKeys(t *testing.T) {
	t.Parallel()

	body := []byte(`{"success":false,"error":true,"message":"Invalid category selected."}`)
	got := ExtractHTTPErrorDetail(body)

	if got != "Invalid category selected." {
		t.Fatalf("unexpected detail: %q", got)
	}
}

func TestExtractHTTPErrorDetailRedactsMessageSecrets(t *testing.T) {
	t.Parallel()

	body := []byte(`{"message":"Upload rejected at https://tracker.example/api?api_token=secret-token"}`)
	got := ExtractHTTPErrorDetail(body)

	if strings.Contains(got, "secret-token") {
		t.Fatalf("expected secret to be redacted, got %q", got)
	}
	if !strings.Contains(got, "api_token=[REDACTED]") {
		t.Fatalf("expected redacted token marker, got %q", got)
	}
}

func TestExtractHTTPErrorDetailFallsBackToCompactBody(t *testing.T) {
	t.Parallel()

	body := []byte("<html><body><h1>Forbidden</h1><p>Upload permission denied.</p></body></html>")
	got := ExtractHTTPErrorDetail(body)

	if got != "Forbidden Upload permission denied." {
		t.Fatalf("unexpected compact body: %q", got)
	}
}

func TestFormatErrorValueStopsAtMaxDepth(t *testing.T) {
	t.Parallel()

	if got := formatErrorValue(nestedMessageValue(maxHTTPErrorDetailDepth-1, "within depth"), "", 0); got != "within depth" {
		t.Fatalf("expected nested value within depth, got %q", got)
	}
	if got := formatErrorValue(nestedMessageValue(maxHTTPErrorDetailDepth, "too deep"), "", 0); got != "" {
		t.Fatalf("expected nested value beyond depth to be skipped, got %q", got)
	}
}

func nestedMessageValue(depth int, message string) any {
	var value any = message
	for range depth {
		value = map[string]any{"message": value}
	}
	return value
}

func TestUploadHTTPErrorWithURLRedactsURLSecrets(t *testing.T) {
	t.Parallel()

	var err error = UploadHTTPErrorWithURL("AITHER", 403, "https://tracker.example/api?api_key=secret-key", nil)
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("expected URL secret to be redacted, got %v", err)
	}
	if !strings.Contains(err.Error(), "api_key=[REDACTED]") {
		t.Fatalf("expected redacted key marker, got %v", err)
	}
}

func TestUploadHTTPErrorIncludesExtractedDetail(t *testing.T) {
	t.Parallel()

	var err error = UploadHTTPError("AITHER", 422, []byte(`{"errors":{"name":["Already exists"]}}`))
	if !strings.Contains(err.Error(), "AITHER upload failed status=422: name: Already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}
