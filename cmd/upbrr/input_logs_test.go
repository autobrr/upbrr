// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/logging"
)

func TestExportLastLogDoesNotProcessInputAndUsesSelectedDirectory(t *testing.T) {
	repo, cfg, _ := newRecoveryCLIFixture(t)
	logger, err := logging.New(config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       2,
	}, repo.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	source := filepath.Join(t.TempDir(), "Missing.Release.2026-GRP.mkv")
	for _, msg := range []string{"previous run detail", "latest run detail"} {
		ctx, _, finish := logger.BeginInputLog(t.Context(), repo, source, "CLI input")
		logging.FromContext(ctx, logger).Tracef("%s", msg)
		finish("", "completed")
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	jsonConfig, err := config.ExportToJSON(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(jsonConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("UA_DEFAULT_DB_PATH", "")
	implicit, err := configstore.DefaultYAMLPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.ExportToYAML(&cfg, implicit); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		configArgs []string
	}{
		{name: "explicit JSON", configArgs: []string{"--config", configPath}},
		{name: "implicit startup config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{"--export-last-log", "--log-export-dir", dir, source}, test.configArgs...)
			result := executeCLIForTest(t.Context(), t, args)
			if result.err != nil || !strings.Contains(result.stdout, "Exported") {
				t.Fatalf("CLI export did not succeed without source bytes: %v, %s", result.err, result.stdout)
			}
			files, err := filepath.Glob(filepath.Join(dir, "*.log"))
			if err != nil || len(files) != 1 {
				t.Fatalf("exports: %v, %v", files, err)
			}
			text, err := os.ReadFile(files[0])
			if err != nil || !strings.Contains(string(text), "latest run detail") || strings.Contains(string(text), "previous run detail") {
				t.Fatal("export did not contain only the most recent run")
			}
		})
	}
}

func TestExportCurrentFailedInputUsesDefaultFolderWithoutFileLogging(t *testing.T) {
	repo, _, configPath := newRecoveryCLIFixture(t)
	path := filepath.Join(t.TempDir(), "Missing.Release.2026-GRP.mkv")
	result := executeCLIForTest(t.Context(), t, []string{
		"--config", configPath, "--unattended", "--export-log", path,
	})
	if result.err == nil || !strings.Contains(result.stdout, "Exported") {
		t.Fatalf("processing failure did not retain its export: err=%v stdout=%q", result.err, result.stdout)
	}
	logPath, err := logging.LogPath(repo.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(logPath), "upbrr-input-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("default-folder exports: %v, %v", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(body), "Coverage: memory") ||
		!strings.Contains(string(body), "Outcome: failed") {
		t.Fatalf("the failed input's memory log was not exported: %v", err)
	}
}

func TestInputLogFlagRejections(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Release.2026-GRP.mkv")
	exportDir := t.TempDir()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "export-last-log requires one input",
			args: []string{"--export-last-log"},
			want: "--export-last-log requires one input and cannot use --export-log",
		},
		{
			name: "export-last-log rejects export-log",
			args: []string{"--export-last-log", "--export-log", source},
			want: "--export-last-log requires one input and cannot use --export-log",
		},
		{
			name: "export-last-log rejects processing flags",
			args: []string{"--export-last-log", "--unattended", source},
			want: "--export-last-log cannot be combined with --unattended",
		},
		{
			name: "log-export-dir requires an export flag",
			args: []string{"--log-export-dir", exportDir, source},
			want: "--log-export-dir requires --export-log or --export-last-log",
		},
		{
			name: "export-log rejects non-processing modes",
			args: []string{"--export-log", "--version"},
			want: "--export-log requires an input processing run",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := executeCLIForTest(t.Context(), t, test.args)
			cliErr, ok := errors.AsType[*cliExitError](result.err)
			if !ok || cliErr.code != 2 || !strings.Contains(cliErr.Error(), test.want) {
				t.Fatalf("args %v => err=%v, want exit code 2 containing %q", test.args, result.err, test.want)
			}
		})
	}
}
