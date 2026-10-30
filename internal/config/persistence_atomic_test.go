// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestYAMLExportPreservesOpenSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows intentionally retains in-place writes and existing security metadata")
	}
	t.Parallel()
	for name, export := range map[string]func(*Config, string) error{
		"encrypted": ExportToYAML,
		"plaintext": ExportToPlaintextYAML,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			cfg := validMinimalConfig()
			if err := export(cfg, path); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			cfg.ScreenshotHandling.Screens = 7
			if err := export(cfg, path); err != nil {
				t.Fatal(err)
			}
			old, err := io.ReadAll(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(old, before) {
				t.Fatal("an already-open snapshot changed during export")
			}
			current, err := ImportFromYAML(path)
			if err != nil {
				t.Fatal(err)
			}
			if current.ScreenshotHandling.Screens != 7 {
				t.Fatal("new readers did not receive the completed export")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("export left temporary files: %d entries", len(entries))
			}
		})
	}
}

func TestYAMLExportFailedPublicationCleansStaging(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	destination := filepath.Join(dir, "config.yaml")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(destination, "keep")
	if err := os.WriteFile(marker, []byte("existing data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExportToPlaintextYAML(validMinimalConfig(), destination); err == nil {
		t.Fatal("export replaced a directory")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing data" {
		t.Fatal("failed publication changed existing data")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("failed export left temporary files: %d entries", len(entries))
	}
}

func TestYAMLExportReplacementPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not implement Unix permission bits")
	}
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old export"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExportToPlaintextYAML(validMinimalConfig(), path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %o, want 600", info.Mode().Perm())
	}
}

func TestYAMLExportRejectsSymlinkDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows retains existing in-place destination semantics")
	}
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("original config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ExportToPlaintextYAML(validMinimalConfig(), link); err == nil {
		t.Fatal("export accepted a symbolic-link destination")
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf("export changed symbolic link: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original config" {
		t.Fatal("export changed symbolic-link target")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("rejected export left temporary files: %d entries", len(entries))
	}
}

func TestYAMLExportRejectsRestrictiveDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission-bit preservation")
	}
	t.Parallel()
	for _, mode := range []os.FileMode{0o400, 0o200, 0} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("original config"), mode); err != nil {
				t.Fatal(err)
			}
			if err := ExportToPlaintextYAML(validMinimalConfig(), path); err == nil {
				t.Fatal("export accepted restrictive destination")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != mode {
				t.Fatal("failed export changed destination permissions")
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "original config" {
				t.Fatal("failed export changed destination contents")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatal("failed export left staging files")
			}
		})
	}
}
