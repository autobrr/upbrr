// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build windows

package livetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateRunDirWindowsCaseVariantsTerminate(t *testing.T) {
	// The regression previously looped forever at the drive root. Run it in
	// a bounded child process so a recurrence cannot strand this test suite.
	const childEnv = "UPBRR_TEST_PROFILE_CASE_CHILD"
	if os.Getenv(childEnv) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestValidateRunDirWindowsCaseVariantsTerminate$")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("case-variant validation did not complete successfully: %v: %s", err, output)
		}
		return
	}

	t.Setenv("LOCALAPPDATA", t.TempDir())
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
	if err := PublishProfile(p); err != nil {
		t.Fatal(err)
	}
	for _, rootVariant := range []string{strings.ToUpper(root), strings.ToLower(root)} {
		variant := filepath.Join(rootVariant, "RuNs", p.RunID)
		got, err := ValidateRunDir(variant)
		if err != nil || got != runDir {
			t.Fatalf("case alias was not normalized: got=%q err=%v", got, err)
		}
		loaded, err := LoadProfile(variant)
		if err != nil || loaded.RunDir != p.RunDir || loaded.RunID != p.RunID {
			t.Fatalf("case alias changed profile identity: %v", err)
		}
		loaded, err = ProfileForDB(filepath.Join(variant, "PrOfIlE", "DB.SQLITE"))
		if err != nil || loaded.DBPath != p.DBPath {
			t.Fatalf("case alias changed database identity: %v", err)
		}
	}
	if _, err := ValidateRunDir(filepath.Join(strings.ToUpper(root), "outside", p.RunID)); err == nil {
		t.Fatal("unapproved case-variant parent accepted")
	}
}
