// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

func TestAuthCLIChangesPasswordWithoutExposingSecrets(t *testing.T) {
	dbPath, configPath := writeAuthCLIConfig(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := executeCLI(t.Context(), []string{"auth", "password", "--config", configPath}, cliIO{
		in:     strings.NewReader("very-secure-password\nreplacement-secure-password\nreplacement-secure-password\n"),
		out:    &stdout,
		errOut: &stderr,
	})
	if err != nil {
		t.Fatalf("auth password: %v", err)
	}
	for _, secret := range []string{"very-secure-password", "replacement-secure-password"} {
		if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
			t.Fatalf("auth password output exposed a password: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
	for _, want := range []string{"Current password: ", "New password: ", "Confirm new password: ", "Password changed."} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("auth password output = %q, want %q", stdout.String(), want)
		}
	}

	record, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	if !authmaterial.VerifyPassword("replacement-secure-password", record.PasswordHash) {
		t.Fatal("replacement password does not verify")
	}
	if authmaterial.VerifyPassword("very-secure-password", record.PasswordHash) {
		t.Fatal("previous password still verifies")
	}
}

func TestAuthCLIRejectsPasswordMismatch(t *testing.T) {
	dbPath, configPath := writeAuthCLIConfig(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	var output bytes.Buffer
	err := executeCLI(t.Context(), []string{"auth", "password", "--config", configPath}, cliIO{
		in:  strings.NewReader("very-secure-password\nreplacement-secure-password\ndifferent-secure-password\n"),
		out: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "passwords do not match") {
		t.Fatalf("auth password error = %v", err)
	}
	for _, secret := range []string{"very-secure-password", "replacement-secure-password", "different-secure-password"} {
		if strings.Contains(output.String(), secret) || strings.Contains(err.Error(), secret) {
			t.Fatalf("auth password mismatch exposed a password: output=%q err=%v", output.String(), err)
		}
	}
	record, loadErr := authmaterial.LoadRecordFromDBPath(dbPath)
	if loadErr != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", loadErr)
	}
	if !authmaterial.VerifyPassword("very-secure-password", record.PasswordHash) {
		t.Fatal("password changed after mismatched confirmation")
	}
}

func TestAuthCLIUpdatesBrowseRoots(t *testing.T) {
	dbPath, configPath := writeAuthCLIConfig(t)
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	first := t.TempDir()
	second := t.TempDir()

	var output bytes.Buffer
	err := executeCLI(t.Context(), []string{"auth", "browse-roots", "--config", configPath, first, second, first}, cliIO{out: &output})
	if err != nil {
		t.Fatalf("auth browse-roots: %v", err)
	}
	if output.String() != "Updated 2 browse roots.\n" {
		t.Fatalf("auth browse-roots output = %q", output.String())
	}
	record, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	if !strings.Contains(record.BrowseRoot, first) || !strings.Contains(record.BrowseRoot, second) || record.AllowUnrestrictedBrowse {
		t.Fatalf(
			"stored browse policy: has_first=%t has_second=%t unrestricted=%t",
			strings.Contains(record.BrowseRoot, first),
			strings.Contains(record.BrowseRoot, second),
			record.AllowUnrestrictedBrowse,
		)
	}

	output.Reset()
	err = executeCLI(
		t.Context(),
		[]string{"auth", "browse-roots", "--config", configPath, "--allow-unrestricted"},
		cliIO{out: &output},
	)
	if err != nil {
		t.Fatalf("auth browse-roots unrestricted: %v", err)
	}
	if output.String() != "Enabled unrestricted host browsing.\n" {
		t.Fatalf("auth browse-roots unrestricted output = %q", output.String())
	}
	record, err = authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath unrestricted: %v", err)
	}
	if record.BrowseRoot != "" || !record.AllowUnrestrictedBrowse {
		t.Fatalf(
			"stored unrestricted browse policy: has_roots=%t unrestricted=%t",
			record.BrowseRoot != "",
			record.AllowUnrestrictedBrowse,
		)
	}
}

func writeAuthCLIConfig(t *testing.T) (string, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "upbrr.db")
	configPath := filepath.Join(tempDir, "config.yaml")
	body := "main_settings:\n  db_path: " + filepath.ToSlash(dbPath) + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dbPath, configPath
}
