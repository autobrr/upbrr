// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package livetest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileRequiresReadyIdentityAndPrivatePaths(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("XDG_CACHE_HOME", base)
	root, err := PrivateRoot()
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "runs", "synthetic-run")
	profileDir := filepath.Join(runDir, "profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Profile{
		Version:         1,
		RunID:           filepath.Base(runDir),
		RunDir:          runDir,
		DBPath:          filepath.Join(profileDir, "db.sqlite"),
		ConfigPath:      filepath.Join(profileDir, "config.yaml"),
		DefaultTrackers: []string{"LST"},
	}
	for _, file := range []string{p.DBPath, p.ConfigPath} {
		if err := os.WriteFile(file, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadProfile(runDir); err == nil {
		t.Fatal("incomplete profile accepted")
	}
	if err := PublishProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := ProfileForDB(p.DBPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ProfileForDB(filepath.Join(profileDir, "other.sqlite")); err == nil {
		t.Fatal("wrong DB identity accepted")
	}
	if _, err := ValidateRunDir(filepath.Join(base, "outside")); err == nil {
		t.Fatal("outside run root accepted")
	}
	if err := os.WriteFile(filepath.Join(runDir, "profile-ready"), []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(runDir); err == nil {
		t.Fatal("marker mismatch accepted")
	}
	if err := os.Remove(p.DBPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p.ConfigPath, p.DBPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := PublishProfile(p); err == nil {
		t.Fatal("symlink database accepted")
	}
}
