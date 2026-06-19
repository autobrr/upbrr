// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
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

func TestCreateCLIAuthFileRejectsShortPassword(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	input := strings.NewReader("tester\nshortpass\nshortpass\n")

	var output strings.Builder
	err := createCLIAuthFile(input, &output, dbPath)
	if err == nil {
		t.Fatal("expected short password validation error")
	}
	if !strings.Contains(err.Error(), "create auth: password too short") {
		t.Fatalf("unexpected error for short password: %v", err)
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

func TestRunHelpFlagsPrintUsageAndSucceed(t *testing.T) {
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()

	for _, helpFlag := range []string{"-help", "--help", "-h", "--h"} {
		t.Run(helpFlag, func(t *testing.T) {
			os.Args = []string{"upbrr", helpFlag}
			output := captureRunStdout(t, func() {
				if err := run(); err != nil {
					t.Fatalf("run: %v", err)
				}
			})
			if !strings.Contains(output, "Usage: upbrr [options] <input path>...") {
				t.Fatalf("expected top-level usage in output, got %q", output)
			}
			for _, expected := range []string{
				"Commands:",
				"  serve [options]",
				"Start the embedded web UI server",
				"Options: --addr, --host, --port, --persist-listen, --dev-no-auth",
				"Config:",
				"Execution:",
				"Tracker Selection:",
				"Release Overrides:",
				"Screenshots and Images:",
				"-config, --config string",
				"-limit-queue, --limit-queue, -lq int",
				"-version, --version",
			} {
				if !strings.Contains(output, expected) {
					t.Fatalf("expected output to contain %q, got %q", expected, output)
				}
			}
			if strings.Contains(output, "-gui") {
				t.Fatalf("expected GUI flag to be absent from help, got %q", output)
			}
		})
	}
}

func TestRunServeHelpPrintsUsageAndSucceeds(t *testing.T) {
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()

	os.Args = []string{"upbrr", "serve", "--help"}
	output := captureRunStdout(t, func() {
		if err := run(); err != nil {
			t.Fatalf("run: %v", err)
		}
	})
	if !strings.Contains(output, "Usage: upbrr serve [options]") {
		t.Fatalf("expected serve usage in output, got %q", output)
	}
	for _, expected := range []string{"Config:", "Server:", "Development:", "-config, --config string", "-addr, --addr string", "-host, --host string", "-port, --port int", "-persist-listen, --persist-listen", "-dev-no-auth, --dev-no-auth"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
}

func TestRunServePersistListenRequiresListenOverride(t *testing.T) {
	err := runServe([]string{"--persist-listen"})
	if err == nil || !strings.Contains(err.Error(), "--persist-listen requires --addr, --host, or --port") {
		t.Fatalf("expected persist-listen requirement error, got %v", err)
	}
}

func TestRunServeRejectedDevelopmentNoAuthHostDoesNotPersistListenOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	body := "main_settings:\n  db_path: " + filepath.ToSlash(dbPath) + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := runServe([]string{"--config", configPath, "--dev-no-auth", "--host", "0.0.0.0", "--persist-listen"})
	if err == nil || !strings.Contains(err.Error(), "--dev-no-auth requires a loopback host") {
		t.Fatalf("expected dev-no-auth loopback error, got %v", err)
	}

	cfg, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		t.Fatalf("load web config: %v", err)
	}
	if cfg.Host != "localhost" || cfg.Port != 7480 {
		t.Fatalf("rejected listen override persisted: %#v", cfg)
	}
}

func TestRunServePersistListenBindFailureDoesNotWriteWebConfig(t *testing.T) {
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fixture: %v", err)
	}
	defer listener.Close()

	tmpDir := t.TempDir()
	distPath := filepath.Join(tmpDir, "gui", "frontend", "dist")
	if err := os.MkdirAll(distPath, 0o755); err != nil {
		t.Fatalf("create web assets fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distPath, "index.html"), []byte("<!doctype html><title>upbrr</title>"), 0o600); err != nil {
		t.Fatalf("write web assets fixture: %v", err)
	}
	t.Chdir(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	body := "main_settings:\n  db_path: " + filepath.ToSlash(dbPath) + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	err = runServe([]string{"--config", configPath, "--dev-no-auth", "--host", "127.0.0.1", "--port", port, "--persist-listen"})
	if err == nil || !strings.Contains(err.Error(), "webserver: listen") {
		t.Fatalf("expected listen error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dbPath), "web-config.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no persisted web config after bind failure, stat error: %v", err)
	}
}

func captureRunStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	stdoutPath := filepath.Join(t.TempDir(), "stdout.txt")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("create stdout fixture: %v", err)
	}
	os.Stdout = stdoutFile
	defer func() {
		os.Stdout = original
	}()

	fn()

	if err := stdoutFile.Close(); err != nil {
		t.Fatalf("close stdout fixture: %v", err)
	}
	raw, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout fixture: %v", err)
	}
	return string(raw)
}

func TestRunWithoutArgsStillRequiresInputPath(t *testing.T) {
	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()

	os.Args = []string{"upbrr"}
	err := run()
	var cliErr *cliExitError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected cliExitError, got %v", err)
	}
	if cliErr.code != 2 {
		t.Fatalf("expected exit code 2, got %d", cliErr.code)
	}
	if !strings.Contains(cliErr.Error(), "at least one input path is required") {
		t.Fatalf("unexpected error: %v", cliErr)
	}
}

func TestRunExportConfigPlaintextExportsPlainSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	configPath := filepath.Join(tmpDir, "config.yaml")
	outputPath := filepath.Join(tmpDir, "export.yaml")

	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(context.Background()); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}

	cfg := &config.Config{
		MainSettings: config.MainSettingsConfig{
			DBPath:  dbPath,
			TMDBAPI: "plain-tmdb-token",
		},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
	}
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.SaveToDatabase(context.Background(), cfg, repo); err != nil {
		t.Fatalf("save config: %v", err)
	}

	oldArgs := os.Args
	defer func() {
		os.Args = oldArgs
	}()

	os.Args = []string{"upbrr", "--config", configPath, "--export-config", outputPath, "--export-config-plaintext"}
	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	exported := string(raw)
	if !strings.Contains(exported, "plain-tmdb-token") {
		t.Fatalf("expected plaintext secret in export, got %s", exported)
	}
	if strings.Contains(exported, "upbrr-enc:v1:") {
		t.Fatalf("expected plaintext export without encrypted envelopes, got %s", exported)
	}
}

func TestPrepareCLIUploadMetadataRefreshesResolvedPathForExternalSelections(t *testing.T) {
	t.Parallel()

	sourcePath := "folder"
	resolvedPath := filepath.Join("folder", "movie.mkv")
	tmdbID := 12345

	coreSvc := &cliCoreForTest{
		previewResponses: []api.MetadataPreview{
			{SourcePath: resolvedPath},
			{SourcePath: resolvedPath},
		},
	}
	req := api.Request{
		Paths: []string{sourcePath},
		ExternalIDSelections: map[string]api.ExternalIDSelection{
			sourcePath: {TMDBID: &tmdbID},
		},
	}

	resolvedReq, err := prepareCLIUploadMetadata(context.Background(), coreSvc, req)
	if err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}
	if len(coreSvc.requests) != 2 {
		t.Fatalf("expected 2 preview requests, got %#v", coreSvc.requests)
	}
	if len(coreSvc.requests[0].req.Paths) != 1 || coreSvc.requests[0].req.Paths[0] != sourcePath {
		t.Fatalf("expected first preview for source path, got %#v", coreSvc.requests[0].req.Paths)
	}
	if len(coreSvc.requests[1].req.Paths) != 1 || coreSvc.requests[1].req.Paths[0] != resolvedPath {
		t.Fatalf("expected second preview for resolved path, got %#v", coreSvc.requests[1].req.Paths)
	}
	if len(resolvedReq.Paths) != 1 || resolvedReq.Paths[0] != resolvedPath {
		t.Fatalf("expected resolved upload path, got %#v", resolvedReq.Paths)
	}
	selected, ok := resolveCLIExternalIDSelection(resolvedReq.ExternalIDSelections, resolvedPath)
	if !ok || selected.TMDBID == nil || *selected.TMDBID != tmdbID {
		t.Fatalf("expected resolved-path external selection, got %#v", resolvedReq.ExternalIDSelections)
	}
	secondSelected, ok := coreSvc.requests[1].req.ExternalIDSelections[resolvedPath]
	if !ok || secondSelected.TMDBID == nil || *secondSelected.TMDBID != tmdbID {
		t.Fatalf("expected resolved-path selection on second preview, got %#v", coreSvc.requests[1].req.ExternalIDSelections)
	}
}

func TestPrepareCLIUploadMetadataPreservesResolvedPathExternalSelections(t *testing.T) {
	t.Parallel()

	sourcePath := "folder"
	resolvedPath := filepath.Join("folder", "movie.mkv")
	currentTMDBID := 12345
	staleTMDBID := 99999

	coreSvc := &cliCoreForTest{
		previewResponses: []api.MetadataPreview{
			{SourcePath: resolvedPath},
			{SourcePath: resolvedPath},
		},
	}
	req := api.Request{
		Paths: []string{sourcePath},
		ExternalIDSelections: map[string]api.ExternalIDSelection{
			sourcePath:   {TMDBID: &currentTMDBID},
			resolvedPath: {TMDBID: &staleTMDBID},
		},
	}

	resolvedReq, err := prepareCLIUploadMetadata(context.Background(), coreSvc, req)
	if err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}

	selected, ok := resolvedReq.ExternalIDSelections[resolvedPath]
	if !ok || selected.TMDBID == nil || *selected.TMDBID != staleTMDBID {
		t.Fatalf("expected resolved upload selection to preserve resolved TMDB ID, got %#v", resolvedReq.ExternalIDSelections)
	}
	secondSelected, ok := coreSvc.requests[1].req.ExternalIDSelections[resolvedPath]
	if !ok || secondSelected.TMDBID == nil || *secondSelected.TMDBID != staleTMDBID {
		t.Fatalf("expected second preview to preserve resolved TMDB ID, got %#v", coreSvc.requests[1].req.ExternalIDSelections)
	}
}
