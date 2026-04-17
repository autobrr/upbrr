// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/webserver"
)

func TestParseCLIOptionsCreateAuth(t *testing.T) {
	t.Parallel()

	opts, visited, paths, err := parseCLIOptions([]string{"--create-auth"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.CreateAuth {
		t.Fatalf("expected create-auth to parse, got %#v", opts)
	}
	if !visited["create-auth"] {
		t.Fatalf("expected create-auth visited flag, got %#v", visited)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no positional paths, got %#v", paths)
	}
}

func TestCreateCLIAuthFileSuccess(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	input := strings.NewReader("tester\nvery-secure-password\nvery-secure-password\n")

	var output strings.Builder
	if err := createCLIAuthFile(input, &output, dbPath); err != nil {
		t.Fatalf("createCLIAuthFile: %v", err)
	}

	authPath := webserver.AuthFilePath(dbPath)
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	if !strings.Contains(string(raw), `"username": "tester"`) {
		t.Fatalf("expected username in auth file, got %s", raw)
	}
	if strings.Contains(string(raw), "very-secure-password") {
		t.Fatalf("auth file leaked plaintext password: %s", raw)
	}
	if got := output.String(); !strings.Contains(got, "Username: ") || !strings.Contains(got, "Password: ") {
		t.Fatalf("expected prompts in output, got %q", got)
	}
}

func TestCreateCLIAuthFileRefusesOverwrite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	input := strings.NewReader("tester\nvery-secure-password\nvery-secure-password\n")
	var output strings.Builder
	err := createCLIAuthFile(input, &output, dbPath)
	if err == nil || !strings.Contains(err.Error(), "user already exists") {
		t.Fatalf("expected existing auth file error, got %v", err)
	}
}

func TestRunCreateAuthUsesConfiguredDBPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "custom", "upbrr.db")
	body := "main_settings:\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldArgs := os.Args
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Args = oldArgs
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	stdinPath := filepath.Join(tmpDir, "stdin.txt")
	if err := os.WriteFile(stdinPath, []byte("tester\nvery-secure-password\nvery-secure-password\n"), 0o600); err != nil {
		t.Fatalf("write stdin fixture: %v", err)
	}
	stdinFile, err := os.Open(stdinPath)
	if err != nil {
		t.Fatalf("open stdin fixture: %v", err)
	}
	defer stdinFile.Close()
	os.Stdin = stdinFile

	stdoutPath := filepath.Join(tmpDir, "stdout.txt")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("create stdout fixture: %v", err)
	}
	defer stdoutFile.Close()
	os.Stdout = stdoutFile

	os.Args = []string{"upbrr", "--create-auth", "--config", configPath}
	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, err := os.Stat(webserver.AuthFilePath(dbPath)); err != nil {
		t.Fatalf("expected auth file beside configured db path: %v", err)
	}
}

func TestRunRejectsCreateAuthConflicts(t *testing.T) {
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()

	os.Args = []string{"upbrr", "--create-auth", "--export-config", "out.yaml"}
	err := run()
	var cliErr *cliExitError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected cliExitError, got %v", err)
	}
	if cliErr.code != 2 {
		t.Fatalf("expected exit code 2, got %d", cliErr.code)
	}
	if !strings.Contains(cliErr.Error(), "--create-auth and --export-config cannot be used together") {
		t.Fatalf("unexpected error: %v", cliErr)
	}
}
