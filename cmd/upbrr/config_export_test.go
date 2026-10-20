// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRunExportConfigFreshInstallation(t *testing.T) {
	for _, plaintext := range []bool{false, true} {
		name := "encrypted"
		if plaintext {
			name = "plaintext"
		}
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", stateDir)
			t.Setenv("UA_DEFAULT_SCREENS", "23")
			t.Setenv("UA_DEFAULT_TMDB_API", "synthetic-env-token")
			outputPath := filepath.Join(t.TempDir(), "nested", "defaults.yaml")
			args := []string{"--export-config", outputPath}
			if plaintext {
				args = append(args, "--export-config-plaintext")
			}
			var stdout, stderr strings.Builder
			if err := executeCLI(t.Context(), args, cliIO{
				in:     strings.NewReader(""),
				out:    &stdout,
				errOut: &stderr,
			}); err != nil {
				t.Fatalf("export fresh defaults: %v", err)
			}
			wantBanner := "upbrr " + cliApplicationVersion(api.CurrentApplicationInfo()) + "\n"
			if !strings.Contains(stdout.String(), "exported config") || stderr.String() != wantBanner {
				t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			raw, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read exported defaults: %v", err)
			}
			if !bytes.Equal(raw, config.EmbeddedExampleYAML()) {
				t.Fatal("export differs from complete embedded template")
			}
			var actual config.Config
			if err := yaml.Unmarshal(raw, &actual); err != nil {
				t.Fatalf("parse exported defaults: %v", err)
			}
			if actual.MainSettings.TMDBAPI != "" || actual.ScreenshotHandling.Screens != 4 || len(actual.Trackers.Trackers) == 0 {
				t.Fatal("default export lost built-in settings or included environment overrides")
			}
			client, ok := actual.TorrentClients["qbittorrent"]
			if !ok || len(client.LinkedFolder) != 1 || client.LinkedFolder[0] != "" || client.AllowFallback == nil || !*client.AllowFallback {
				t.Fatal("default export lost editable client placeholders or defaults")
			}

			dbPath, err := db.DefaultPath()
			if err != nil {
				t.Fatalf("database path: %v", err)
			}
			repo, err := db.OpenContext(t.Context(), dbPath)
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			defer repo.Close()
			var stored config.Config
			if err := repo.LoadFullConfig(t.Context(), &stored); !errors.Is(err, internalerrors.ErrNotFound) {
				t.Fatalf("default export persisted configuration: %v", err)
			}
			activation, err := repo.LoadConfigActivation(t.Context())
			if err != nil {
				t.Fatalf("load activation: %v", err)
			}
			if activation.ActiveGeneration != 0 || activation.Fingerprint != "" {
				t.Fatal("default export activated configuration")
			}
			if _, err := os.Stat(webserver.AuthFilePath(dbPath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected authentication file: %v", err)
			}
		})
	}
}

func TestRunExportConfigPreservesConfiguredExport(t *testing.T) {
	for _, plaintext := range []bool{false, true} {
		name := "encrypted"
		if plaintext {
			name = "plaintext"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("UA_DEFAULT_SCREENS", "9")
			t.Setenv("UA_DEFAULT_TMDB_API", "")
			dbPath, err := db.DefaultPath()
			if err != nil {
				t.Fatal(err)
			}
			repo, err := db.OpenContext(t.Context(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := webserver.BootstrapAuthFile(dbPath, "tester", "synthetic-test-password"); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadEmbeddedDefaultConfig()
			if err != nil {
				t.Fatal(err)
			}
			cfg.MainSettings.DBPath = dbPath
			cfg.MainSettings.TMDBAPI = "synthetic-stored-token"
			cfg.MainSettings.InputHistoryLimit = 31
			if err := config.SaveToDatabase(t.Context(), cfg, repo); err != nil {
				t.Fatal(err)
			}
			outputPath := filepath.Join(t.TempDir(), "configured.yaml")
			args := []string{"--export-config", outputPath}
			if plaintext {
				args = append(args, "--export-config-plaintext")
			}
			if err := executeCLI(t.Context(), args, cliIO{}); err != nil {
				t.Fatal(err)
			}
			actual, err := config.ImportFromYAML(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if actual.MainSettings.TMDBAPI != cfg.MainSettings.TMDBAPI || actual.MainSettings.InputHistoryLimit != 31 || actual.ScreenshotHandling.Screens != 9 {
				t.Fatal("configured export lost saved values or environment overrides")
			}
			raw, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), cfg.MainSettings.TMDBAPI) != plaintext {
				t.Fatal("configured export did not preserve explicit plaintext selection")
			}
			stored, err := config.LoadFromDatabase(t.Context(), repo)
			if err != nil {
				t.Fatal(err)
			}
			if stored.ScreenshotHandling.Screens != cfg.ScreenshotHandling.Screens {
				t.Fatal("export persisted environment overrides")
			}
		})
	}
}

func TestRunExportConfigDoesNotReplaceDatabaseErrorsWithDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dbPath, err := db.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "defaults.yaml")
	if err := executeCLI(t.Context(), []string{"--export-config", outputPath}, cliIO{}); err == nil {
		t.Fatal("expected corrupt database failure")
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected export after failure: %v", err)
	}
}

func TestRunExportConfigFreshOutputFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	outputPath := t.TempDir()
	if err := executeCLI(t.Context(), []string{"--export-config", outputPath}, cliIO{}); err == nil {
		t.Fatal("expected directory destination failure")
	}
}

func TestRunExportConfigDoesNotReplaceStoredLoadErrorsWithDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dbPath, err := db.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveConfigSection(t.Context(), "MainSettings", map[string]any{"InputHistoryLimit": "invalid-integer"}); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "defaults.yaml")
	if err := executeCLI(t.Context(), []string{"--export-config", outputPath}, cliIO{}); err == nil {
		t.Fatal("expected malformed stored config failure")
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected export after failure: %v", err)
	}
}

func TestRunExportConfigMissingExplicitConfigFails(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	outputPath := filepath.Join(t.TempDir(), "defaults.yaml")
	missingPath := filepath.Join(t.TempDir(), "missing.yaml")
	if err := executeCLI(t.Context(), []string{"--config", missingPath, "--export-config", outputPath}, cliIO{}); err == nil {
		t.Fatal("expected missing explicit config failure")
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected export after failure: %v", err)
	}
}
