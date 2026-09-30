// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dbfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
)

func TestWriteMigratedIsolationAndReopen(t *testing.T) {
	// Concurrent callers share only the immutable template bytes. Each copy must
	// remain writable, isolated, and durable through the production open path.
	for index := range 8 {
		t.Run(fmt.Sprintf("copy-%d", index), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "fixture.db")
			WriteMigrated(t, path)
			repo := openFixture(t, path)
			var value string
			if err := repo.LoadConfigSection(t.Context(), "fixture-isolation", &value); err == nil {
				t.Fatal("new fixture contains another caller's data")
			}
			want := t.Name()
			if err := repo.SaveConfigSection(t.Context(), "fixture-isolation", want); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openFixture(t, path)
			if err := reopened.LoadConfigSection(t.Context(), "fixture-isolation", &value); err != nil {
				t.Fatal(err)
			}
			if value != want {
				t.Fatalf("reopened value = %q, want %q", value, want)
			}
		})
	}
}

func TestWriteMigratedMatchesFreshSchema(t *testing.T) {
	t.Parallel()
	fresh := openFixture(t, filepath.Join(t.TempDir(), "fresh.db"))
	if err := fresh.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "copy.db")
	WriteMigrated(t, path)
	copyRepo := openFixture(t, path)
	if got, want := schema(t, copyRepo), schema(t, fresh); !reflect.DeepEqual(got, want) {
		t.Fatalf("copied schema differs from fresh migrations:\ngot %v\nwant %v", got, want)
	}
	if got, want := migrationIDs(t, copyRepo), migrationIDs(t, fresh); !reflect.DeepEqual(got, want) {
		t.Fatalf("copied migration IDs = %v, want %v", got, want)
	}
	var integrity string
	if err := copyRepo.RawDB().QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("copied database integrity = %q", integrity)
	}
}

func openFixture(t testing.TB, path string) *db.SQLiteRepository {
	t.Helper()
	repo, err := db.OpenContext(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func schema(t *testing.T, repo *db.SQLiteRepository) []string {
	t.Helper()
	return queryStrings(t, repo, "SELECT type || ':' || name || ':' || COALESCE(sql, '') FROM sqlite_schema ORDER BY type, name")
}

func migrationIDs(t *testing.T, repo *db.SQLiteRepository) []string {
	t.Helper()
	return queryStrings(t, repo, "SELECT id FROM schema_migrations ORDER BY id")
}

func queryStrings(t *testing.T, repo *db.SQLiteRepository, query string) []string {
	t.Helper()
	rows, err := repo.RawDB().QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

// BenchmarkDatabaseSetup compares real migration setup with a private fixture
// copy, including opening and closing the repository in both cases. Benchmark
// calibration warms the process-local template; use fresh test processes for
// end-to-end cold-template comparisons.
func BenchmarkDatabaseSetup(b *testing.B) {
	for _, migrated := range []bool{false, true} {
		name := "fresh-migrations"
		if migrated {
			name = "template-copy"
		}
		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				path := filepath.Join(dir, fmt.Sprintf("fixture-%d.db", index))
				if migrated {
					WriteMigrated(b, path)
				}
				repo, err := db.OpenContext(b.Context(), path)
				if err != nil {
					b.Fatal(err)
				}
				if !migrated {
					if err := repo.MigrateContext(b.Context()); err != nil {
						_ = repo.Close()
						b.Fatal(err)
					}
				}
				if err := repo.Close(); err != nil {
					b.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
