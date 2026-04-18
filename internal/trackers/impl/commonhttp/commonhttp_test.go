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

func TestLoadCookiesForTrackerPrefersCookieStore(t *testing.T) {
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
