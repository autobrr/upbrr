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

func TestDefaultPathFallsBackToLegacyHomeDir(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, defaultDirName, defaultDBName)
	writeMarkerFile(t, legacy)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got != legacy {
		t.Fatalf("got %q want legacy %q", got, legacy)
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
