// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
)

func TestSnapshotUsesExistingKeyWithoutWrites(t *testing.T) {
	t.Parallel()
	db := newTestCookieDB(t)
	db.SetMaxOpenConns(1)
	path := writeWebAuthFile(t, "synthetic-user", "synthetic-hash")
	key, err := NewKeyManager(db).InitializeEncryptionKey(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for tracker, value := range map[string]string{"AZ": "selected-session", "CZ": "other-session"} {
		if err := store.SaveCookie(t.Context(), tracker, "session", value, key); err != nil {
			t.Fatal(err)
		}
	}
	// Even initial auth-state normalization is forbidden during a draft probe.
	if _, err := db.ExecContext(t.Context(), `DELETE FROM config_settings WHERE section = ?`, cookieAuthStateConfigSection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotTrackerCookieMap(t.Context(), db, path, "AZ", "avistaz.to")
	if err != nil || len(got) != 1 || got["session"] != "selected-session" {
		t.Fatalf("snapshot=%v err=%v", got, err)
	}
	got["session"] = "changed-only-in-memory"
	stored, err := store.GetAllTrackerCookies(t.Context(), "AZ", key)
	if err != nil || stored["session"] != "selected-session" {
		t.Fatal("snapshot changed persisted cookies")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM config_settings WHERE section = ?`, cookieAuthStateConfigSection).Scan(&count); err != nil || count != 0 {
		t.Fatal("snapshot initialized auth state")
	}
}

func TestSnapshotDoesNotInitializeEmptyStorage(t *testing.T) {
	t.Parallel()
	db := newTestCookieDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	_, err := SnapshotTrackerCookieMap(t.Context(), db, filepath.Join(t.TempDir(), "absent", "must-not-create.db"), "AZ", "avistaz.to")
	if !errors.Is(err, ErrTrackerCookiesNotFound) {
		t.Fatalf("empty snapshot: %v", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM config_settings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("snapshot initialized encryption metadata")
	}
}

func TestSnapshotFiltersLegacyCookieDomains(t *testing.T) {
	t.Parallel()
	db := newTestCookieDB(t)
	path := filepath.Join(t.TempDir(), "server.db")
	candidate := commonhttp.CookiePathCandidates(path, "AZ", ".txt")[0]
	if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := "# Netscape HTTP Cookie File\n.avistaz.to\tTRUE\t/\tTRUE\t2147483647\tsession\tselected\n.other.example\tTRUE\t/\tTRUE\t2147483647\tunrelated\tmust-not-send\n"
	if err := os.WriteFile(candidate, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotTrackerCookieMap(t.Context(), db, path, "AZ", "avistaz.to")
	if err != nil || len(got) != 1 || got["session"] != "selected" {
		t.Fatalf("scoped snapshot=%v err=%v", got, err)
	}
}

func TestSnapshotLegacyFallbackPreservesProvenance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                            string
		missingSalt, missingHelper, mismatch, malformed bool
		legacy                                          bool
		want                                            string
	}{
		{
			name:        "missing salt",
			missingSalt: true,
			legacy:      true,
			want:        "legacy-session",
		},
		{
			name:          "missing helper",
			missingHelper: true,
			legacy:        true,
			want:          "legacy-session",
		},
		{name: "missing salt without legacy", missingSalt: true},
		{name: "missing helper without legacy", missingHelper: true},
		{
			name:     "mismatching helper",
			mismatch: true,
			legacy:   true,
		},
		{
			name:        "mismatching helper with missing salt",
			mismatch:    true,
			missingSalt: true,
			legacy:      true,
		},
		{
			name:      "malformed helper",
			malformed: true,
			legacy:    true,
		},
		{
			name:   "stored cookies override legacy",
			legacy: true,
			want:   "stored-session",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestCookieDB(t)
			db.SetMaxOpenConns(1)
			path := writeWebAuthFile(t, "synthetic-user", "synthetic-hash")
			key, err := NewKeyManager(db).InitializeEncryptionKey(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewCookieStore(db)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveCookie(t.Context(), "AZ", "session", "stored-session", key); err != nil {
				t.Fatal(err)
			}
			if tc.missingSalt {
				if _, err := db.ExecContext(t.Context(), `DELETE FROM config_settings WHERE section = ?`, cookieKeyConfigSection); err != nil {
					t.Fatal(err)
				}
			}
			if tc.missingHelper {
				if err := os.Remove(authmaterial.AuthFilePath(path)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mismatch {
				if err := storeAuthStateInDB(t.Context(), db, authState{Fingerprint: "synthetic-unmatched-fingerprint"}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.malformed {
				if err := os.WriteFile(authmaterial.AuthFilePath(path), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy {
				candidate := commonhttp.CookiePathCandidates(path, "AZ", ".txt")[0]
				if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
					t.Fatal(err)
				}
				contents := "# Netscape HTTP Cookie File\n.avistaz.to\tTRUE\t/\tTRUE\t2147483647\tsession\tlegacy-session\n.other.example\tTRUE\t/\tTRUE\t2147483647\tother\tmust-not-send\n"
				if err := os.WriteFile(candidate, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(t.Context(), `PRAGMA query_only=ON`); err != nil {
				t.Fatal(err)
			}
			got, err := SnapshotTrackerCookieMap(t.Context(), db, path, "AZ", "avistaz.to")
			if tc.want == "" {
				if err == nil || len(got) != 0 {
					t.Fatal("unusable stored authentication must not release legacy cookies")
				}
			} else if err != nil || len(got) != 1 || got["session"] != tc.want {
				t.Fatalf("snapshot fallback count=%d err=%v", len(got), err)
			}
		})
	}
}
