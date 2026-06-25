// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMarkerFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestDefaultPathPrefersXDGCanonical(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want := filepath.Join(xdg, "upbrr", defaultDBName)
	writeMarkerFile(t, want)
	// A legacy dir also present must not win over the canonical path.
	writeMarkerFile(t, filepath.Join(xdg, defaultDirName, defaultDBName))

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultPathFallsBackToLegacyDottedDirUnderXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	legacy := filepath.Join(xdg, defaultDirName, defaultDBName)
	writeMarkerFile(t, legacy)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != legacy {
		t.Fatalf("got %q want legacy %q", got, legacy)
	}
}

func TestDefaultPathIgnoresDistinctLegacyHomeDirWhenXDGSet(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, defaultDirName, defaultDBName)
	writeMarkerFile(t, legacy)
	want := filepath.Join(xdg, "upbrr", defaultDBName)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != want {
		t.Fatalf("got %q want canonical %q", got, want)
	}
}

func TestDefaultPathNewInstallUsesXDGCanonical(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir()) // no legacy DB anywhere
	want := filepath.Join(xdg, "upbrr", defaultDBName)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultPathPreservesXDGWhitespace(t *testing.T) {
	xdg := " " + filepath.Join(t.TempDir(), "xdg") + " "
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir())
	want := filepath.Join(xdg, "upbrr", defaultDBName)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDBFileExistsRejectsSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), defaultDBName)
	writeMarkerFile(t, target)
	link := filepath.Join(t.TempDir(), defaultDBName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if dbFileExists(link) {
		t.Fatalf("expected symlink %q not to count as regular DB file", link)
	}
}

func TestSQLiteRepositoryDBPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if repo.DBPath() != path {
		t.Fatalf("DBPath got %q want %q", repo.DBPath(), path)
	}
}

func TestSQLiteRepositoryDBPathUsesResolvedDefault(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir())
	want := filepath.Join(xdg, "upbrr", defaultDBName)

	repo, err := Open("")
	if err != nil {
		t.Fatalf("open default: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if repo.DBPath() != want {
		t.Fatalf("DBPath got %q want resolved default %q", repo.DBPath(), want)
	}
}

func TestSQLiteRepositoryDBPathEmptyForSentinelInputs(t *testing.T) {
	for _, path := range []string{":memory:", "file:memdb1?mode=memory&cache=shared"} {
		t.Run(path, func(t *testing.T) {
			repo, err := Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if repo.DBPath() != "" {
				t.Fatalf("DBPath got %q want empty for sentinel input %q", repo.DBPath(), path)
			}
		})
	}
}
