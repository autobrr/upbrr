// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProcessCLIPathsQueueModeContinuesOnError(t *testing.T) {
	t.Parallel()

	paths := []string{"a", "b", "c"}
	attempted := make([]string, 0, len(paths))
	err := processCLIPaths(context.Background(), paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		attempted = append(attempted, sourcePath)
		if sourcePath == "b" {
			return errors.New("boom")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected a summary error when a queue item fails")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if len(attempted) != len(paths) {
		t.Fatalf("expected all %d items attempted in queue mode, got %v", len(paths), attempted)
	}
}

func TestProcessCLIPathsQueueModeSucceedsWhenAllPass(t *testing.T) {
	t.Parallel()

	if err := processCLIPaths(context.Background(), []string{"a", "b"}, true, time.Minute, api.NopLogger{}, func(context.Context, string) error {
		return nil
	}); err != nil {
		t.Fatalf("expected nil error when all queue items succeed, got %v", err)
	}
}

func TestProcessCLIPathsNonQueueAbortsOnFirstError(t *testing.T) {
	t.Parallel()

	paths := []string{"a", "b", "c"}
	attempted := 0
	err := processCLIPaths(context.Background(), paths, false, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		attempted++
		if sourcePath == "b" {
			return errors.New("boom")
		}
		return nil
	})
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if attempted != 2 {
		t.Fatalf("expected abort after the failing item (2 attempts), got %d", attempted)
	}
}

func TestProcessCLIPathsAppliesPerItemTimeout(t *testing.T) {
	t.Parallel()

	paths := []string{"slow", "fast"}
	fastSawFreshDeadline := false
	err := processCLIPaths(context.Background(), paths, true, 20*time.Millisecond, api.NopLogger{}, func(itemCtx context.Context, sourcePath string) error {
		if sourcePath == "slow" {
			// Exceed the per-item timeout; this item should be cancelled at
			// ~20ms rather than running to completion.
			select {
			case <-itemCtx.Done():
				return itemCtx.Err()
			case <-time.After(2 * time.Second):
				return nil
			}
		}
		// The next item must receive a fresh per-item deadline, proving the
		// timeout is not shared across the queue.
		if deadline, ok := itemCtx.Deadline(); ok && time.Until(deadline) > 5*time.Millisecond {
			fastSawFreshDeadline = true
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected a summary error because the slow item timed out")
	}
	if !fastSawFreshDeadline {
		t.Fatal("expected the second item to get a fresh per-item deadline")
	}
}

// --- GROUP B #5: the per-item context is threaded through to core calls ---

func TestRunSiteCheckCLIPathThreadsContextToCoreCall(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coreSvc := &cliCoreForTest{}
	err := runSiteCheckCLIPath(ctx, coreSvc, cliOptions{}, map[string]bool{}, "movie.mkv", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context to reach FetchMetadataPreview, got %v", err)
	}
}

func TestRunInteractiveCLIPathThreadsContextToCoreCall(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coreSvc := &cliCoreForTest{}
	err := runInteractiveCLIPathWithInput(ctx, coreSvc, nil, cliOptions{}, map[string]bool{}, "movie.mkv", 0, config.Config{}, strings.NewReader(""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context to reach FetchMetadataPreview, got %v", err)
	}
}

func TestProcessCLIPathsAbortsOnParentCancel(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	paths := []string{"a", "b", "c", "d"}
	attempted := 0
	err := processCLIPaths(parent, paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		attempted++
		// Cancel the parent mid-run; remaining items must NOT be attempted as
		// spurious per-item failures.
		if sourcePath == "b" {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected an abort error after parent cancellation")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped context.Canceled, got %v", err)
	}
	if attempted != 2 {
		t.Fatalf("expected the queue to stop after the cancel (2 attempts), got %d", attempted)
	}
}

func TestProcessCLIPathsAbortsWhenLastItemCanceled(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	paths := []string{"only"}
	err := processCLIPaths(parent, paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, _ string) error {
		// Parent cancellation during the final item must abort, not be recorded as
		// a normal queue failure (no next iteration runs to catch it).
		cancel()
		return context.Canceled
	})
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped context.Canceled, got %v", err)
	}
	if strings.Contains(err.Error(), "queue completed") {
		t.Fatalf("expected an abort error, not a normal queue summary, got %v", err)
	}
}

func TestProcessCLIPathsItemTimeoutDoesNotAbortQueue(t *testing.T) {
	t.Parallel()

	// A per-item timeout (on the derived itemCtx) must be treated as an ordinary
	// failure that continues the queue, not as a parent-cancellation abort.
	paths := []string{"slow", "ok"}
	attempted := 0
	err := processCLIPaths(context.Background(), paths, true, 10*time.Millisecond, api.NopLogger{}, func(itemCtx context.Context, sourcePath string) error {
		attempted++
		if sourcePath == "slow" {
			<-itemCtx.Done()
			return itemCtx.Err()
		}
		return nil
	})
	if attempted != len(paths) {
		t.Fatalf("expected all items attempted despite an item timeout, got %d", attempted)
	}
	if err == nil || !strings.Contains(err.Error(), "queue completed") {
		t.Fatalf("expected a normal queue summary error, got %v", err)
	}
	if strings.Contains(err.Error(), "aborted after") {
		t.Fatalf("item timeout must not abort the queue, got %v", err)
	}
}

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
				"Options: --addr, --host, --port, --base-url, --persist-web-config, --dev-no-auth",
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
	for _, expected := range []string{"Config:", "Server:", "Development:", "-config, --config string", "-addr, --addr string", "-host, --host string", "-port, --port int", "-base-url, --base-url string", "-persist-listen, --persist-listen", "-persist-web-config, --persist-web-config", "-dev-no-auth, --dev-no-auth"} {
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

func TestRunServePersistWebConfigRequiresWebConfigOverride(t *testing.T) {
	err := runServe([]string{"--persist-web-config"})
	if err == nil || !strings.Contains(err.Error(), "--persist-web-config requires --addr, --host, --port, --base-url, or UPBRR_WEB_* env") {
		t.Fatalf("expected persist-web-config requirement error, got %v", err)
	}
}

func TestServePersistConfigListenOnlyClearsStoredBaseURL(t *testing.T) {
	stored := webserver.CLIConfig{
		Host:           "localhost",
		Port:           7480,
		OpenBrowser:    true,
		TrustedProxies: []string{"127.0.0.1"},
		BaseURL:        "/stored/",
		SessionTTL:     1440,
	}
	runtime := stored
	runtime.Host = "0.0.0.0"
	runtime.Port = 9090
	runtime.BaseURL = "/temporary/"

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "addr": true, "base-url": true})
	if persisted.Host != "0.0.0.0" || persisted.Port != 9090 {
		t.Fatalf("listen settings not persisted: %#v", persisted)
	}
	if persisted.BaseURL != "" {
		t.Fatalf("base url persisted during listen-only save: %#v", persisted)
	}
	if !persisted.OpenBrowser || persisted.SessionTTL != 1440 || len(persisted.TrustedProxies) != 1 || persisted.TrustedProxies[0] != "127.0.0.1" {
		t.Fatalf("unrelated web config changed: %#v", persisted)
	}
}

func TestServePersistConfigWebConfigPersistsBaseURL(t *testing.T) {
	stored := webserver.CLIConfig{Host: "localhost", Port: 7480, BaseURL: "/stored/"}
	runtime := stored
	runtime.Host = "0.0.0.0"
	runtime.Port = 9090
	runtime.BaseURL = "/explicit/"

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "persist-web-config": true, "addr": true, "base-url": true})
	if persisted.Host != "0.0.0.0" || persisted.Port != 9090 || persisted.BaseURL != "/explicit/" {
		t.Fatalf("explicit web config not persisted: %#v", persisted)
	}
}

func TestServePersistConfigListenOnlyPersistsExplicitListenFields(t *testing.T) {
	stored := webserver.CLIConfig{Host: "localhost", Port: 7480, BaseURL: "/stored/"}
	runtime := webserver.CLIConfig{Host: "0.0.0.0", Port: 9090, BaseURL: "/env/"}

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "port": true})
	if persisted.Host != "localhost" || persisted.Port != 9090 || persisted.BaseURL != "" {
		t.Fatalf("listen persistence included env/transient fields: %#v", persisted)
	}
}

func TestServePersistConfigListenOnlyIgnoresInvalidStoredBaseURLWhenSaving(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	stored := webserver.CLIConfig{Host: "localhost", Port: 7480, BaseURL: "javascript:alert(1)"}
	runtime := webserver.CLIConfig{Host: "127.0.0.1", Port: 9090, BaseURL: "javascript:alert(1)"}

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "host": true})
	if err := webserver.SaveCLIConfig(dbPath, persisted); err != nil {
		t.Fatalf("SaveCLIConfig with listen-only config: %v", err)
	}

	saved, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		t.Fatalf("LoadCLIConfig: %v", err)
	}
	if saved.Host != "127.0.0.1" || saved.Port != 7480 || saved.BaseURL != "" {
		t.Fatalf("saved listen-only config = %#v", saved)
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

// --- GROUP D #7/#8: queue summary error names quoted paths and wraps first cause ---

func TestProcessCLIPathsQueueModeErrorIncludesQuotedFailedPathsAndWrapsCause(t *testing.T) {
	t.Parallel()

	boomFirst := errors.New("first boom")
	boomThird := errors.New("third boom")
	paths := []string{"a,b", "good", "c d"}
	err := processCLIPaths(context.Background(), paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		switch sourcePath {
		case "a,b":
			return boomFirst
		case "c d":
			return boomThird
		default:
			return nil
		}
	})
	if err == nil {
		t.Fatal("expected a summary error when queue items fail")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if !errors.Is(err, boomFirst) {
		t.Fatalf("expected wrapped error to be the FIRST cause, got %v", err)
	}
	if errors.Is(err, boomThird) {
		t.Fatalf("did not expect the later cause to be wrapped, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `"a,b"`) || !strings.Contains(msg, `"c d"`) {
		t.Fatalf("expected quoted failed paths in error message, got %q", msg)
	}
	if !strings.Contains(msg, "2 of 3") {
		t.Fatalf("expected count substring in error message, got %q", msg)
	}
}

// --- GROUP B #5: runCLIPathWithTimeout per-item deadline and parent cancel propagation ---

func TestRunCLIPathWithTimeoutAppliesPerItemDeadline(t *testing.T) {
	t.Parallel()

	err := runCLIPathWithTimeout(context.Background(), 20*time.Millisecond, "p", func(itemCtx context.Context, _ string) error {
		<-itemCtx.Done()
		return itemCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestRunCLIPathWithTimeoutPropagatesParentCancel(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCLIPathWithTimeout(parent, time.Hour, "p", func(itemCtx context.Context, _ string) error {
		return itemCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Canceled to propagate from parent, got %v", err)
	}
}

// --- GROUP A: upload-only queue continue-on-error + bounded-timeout batch ---

func TestRunCLIUploadOnlyQueueContinuesOnError(t *testing.T) {
	t.Parallel()

	failOn := "bad"
	uploadPaths := make([]string, 0)
	coreSvc := &cliCoreForTest{
		runUploadFunc: func(_ context.Context, req api.Request) (api.Result, error) {
			if len(req.Paths) > 0 {
				uploadPaths = append(uploadPaths, req.Paths[0])
				if req.Paths[0] == failOn {
					return api.Result{}, errors.New("upload boom")
				}
			}
			return api.Result{UploadedCount: 1}, nil
		},
	}
	paths := []string{"good1", failOn, "good2"}
	err := runCLIUploadOnlyQueue(context.Background(), coreSvc, api.Request{}, paths, false, api.NopLogger{})
	if err == nil {
		t.Fatal("expected a summary error when one queue item fails")
	}
	if coreSvc.runUploadPreparedCalls != len(paths) {
		t.Fatalf("expected RunUploadPrepared called once per path (%d), got %d", len(paths), coreSvc.runUploadPreparedCalls)
	}
	if len(uploadPaths) != len(paths) {
		t.Fatalf("expected each path attempted (no early abort), got %v", uploadPaths)
	}
}

func TestRunCLIUploadOnlyQueueSumsUploadedCounts(t *testing.T) {
	t.Parallel()

	coreSvc := &cliCoreForTest{}
	paths := []string{"a", "b", "c"}
	err := runCLIUploadOnlyQueue(context.Background(), coreSvc, api.Request{}, paths, false, api.NopLogger{})
	if err != nil {
		t.Fatalf("expected no error for all-passing queue, got %v", err)
	}
	if coreSvc.runUploadPreparedCalls != len(paths) {
		t.Fatalf("expected %d RunUploadPrepared calls, got %d", len(paths), coreSvc.runUploadPreparedCalls)
	}
	for _, r := range coreSvc.requests {
		if r.name != "upload" {
			continue
		}
		if len(r.req.Paths) != 1 {
			t.Fatalf("expected single-path upload request per item, got %#v", r.req.Paths)
		}
	}
}

func TestRunCLIUploadOnlyBatchBoundsTimeoutByItemCap(t *testing.T) {
	t.Parallel()

	var remaining time.Duration
	var sawDeadline bool
	coreSvc := &cliCoreForTest{
		runUploadFunc: func(ctx context.Context, _ api.Request) (api.Result, error) {
			if deadline, ok := ctx.Deadline(); ok {
				sawDeadline = true
				remaining = time.Until(deadline)
			}
			return api.Result{UploadedCount: 1}, nil
		},
	}
	paths := make([]string, cliUploadOnlyTimeoutCap+25)
	for i := range paths {
		paths[i] = "p" + strconv.Itoa(i)
	}
	if err := runCLIUploadOnlyBatch(context.Background(), coreSvc, api.Request{Paths: paths}, paths, false); err != nil {
		t.Fatalf("runCLIUploadOnlyBatch: %v", err)
	}
	if !sawDeadline {
		t.Fatal("expected the upload context to carry a deadline")
	}
	capDur := time.Duration(cliUploadOnlyTimeoutCap) * cliItemTimeout
	if remaining > capDur {
		t.Fatalf("expected deadline <= cap %v, got remaining %v", capDur, remaining)
	}
	if remaining >= time.Duration(len(paths))*cliItemTimeout {
		t.Fatalf("expected bounded deadline below len(paths)*cliItemTimeout, got %v", remaining)
	}
	if coreSvc.runUploadPreparedCalls != 1 {
		t.Fatalf("expected a single batch RunUploadPrepared call, got %d", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunCLIUploadOnlyBatchSinglePathTimeout(t *testing.T) {
	t.Parallel()

	var remaining time.Duration
	var sawDeadline bool
	coreSvc := &cliCoreForTest{
		runUploadFunc: func(ctx context.Context, _ api.Request) (api.Result, error) {
			if deadline, ok := ctx.Deadline(); ok {
				sawDeadline = true
				remaining = time.Until(deadline)
			}
			return api.Result{UploadedCount: 1}, nil
		},
	}
	paths := []string{"only"}
	if err := runCLIUploadOnlyBatch(context.Background(), coreSvc, api.Request{Paths: paths}, paths, false); err != nil {
		t.Fatalf("runCLIUploadOnlyBatch: %v", err)
	}
	if !sawDeadline {
		t.Fatal("expected the upload context to carry a deadline")
	}
	if remaining > cliItemTimeout {
		t.Fatalf("expected single-path deadline <= cliItemTimeout, got %v", remaining)
	}
	if coreSvc.runUploadPreparedCalls != 1 {
		t.Fatalf("expected a single RunUploadPrepared call, got %d", coreSvc.runUploadPreparedCalls)
	}
}

// --- GROUP C: BDMV setup cancellation is surfaced, not swallowed ---

func newBDMVTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "BDMV"), 0o755); err != nil {
		t.Fatalf("mkdir BDMV: %v", err)
	}
	return root
}

func TestHandleBDMVPlaylistSelectionReturnsOnDetectDiscTypeCancel(t *testing.T) {
	t.Parallel()

	root := newBDMVTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := handleBDMVPlaylistSelection(ctx, []string{root}, &cliCoreForTest{}, config.Config{}, api.NopLogger{}, cliOptions{})
	if err == nil || !isCtxErr(err) {
		t.Fatalf("expected a context error from DetectDiscType, got %v", err)
	}
}

func TestHandleBDMVPlaylistSelectionReturnsOnLoadSelectionCtxErr(t *testing.T) {
	t.Parallel()

	root := newBDMVTempDir(t)
	coreSvc := &cliCoreForTest{playlistSelectionErr: context.DeadlineExceeded}
	err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{}, api.NopLogger{}, cliOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded from LoadPlaylistSelection, got %v", err)
	}
}

func TestHandleBDMVPlaylistSelectionReturnsOnDiscoverPlaylistsCtxErr(t *testing.T) {
	t.Parallel()

	for _, useLargest := range []bool{true, false} {
		root := newBDMVTempDir(t)
		coreSvc := &cliCoreForTest{
			playlistSelectionErr: internalerrors.ErrNotFound,
			discoverPlaylistsErr: context.Canceled,
		}
		err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{
			Metadata: config.MetadataConfig{UseLargestPlaylist: useLargest},
		}, api.NopLogger{}, cliOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("UseLargestPlaylist=%v: expected Canceled from DiscoverPlaylists, got %v", useLargest, err)
		}
	}
}

func TestHandleBDMVPlaylistSelectionReturnsOnSaveSelectionCtxErr(t *testing.T) {
	t.Parallel()

	for _, useLargest := range []bool{true, false} {
		root := newBDMVTempDir(t)
		coreSvc := &cliCoreForTest{
			playlistSelectionErr: internalerrors.ErrNotFound,
			playlists:            []api.PlaylistInfo{{File: "00800.mpls", Score: 1}},
			savePlaylistErr:      context.Canceled,
		}
		err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{
			Metadata: config.MetadataConfig{UseLargestPlaylist: useLargest},
		}, api.NopLogger{}, cliOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("UseLargestPlaylist=%v: expected Canceled from SavePlaylistSelection, got %v", useLargest, err)
		}
	}
}

func TestHandleBDMVPlaylistSelectionSkipsNonCtxErrors(t *testing.T) {
	t.Parallel()

	root := newBDMVTempDir(t)
	coreSvc := &cliCoreForTest{playlistSelectionErr: errors.New("boom")}
	err := handleBDMVPlaylistSelection(context.Background(), []string{root}, coreSvc, config.Config{}, api.NopLogger{}, cliOptions{})
	if err != nil {
		t.Fatalf("expected non-ctx load error to be skipped (continue), got %v", err)
	}
}
