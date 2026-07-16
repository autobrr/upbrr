// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

func runCLIUploadOnlyQueueForPaths(
	ctx context.Context,
	coreSvc cliUploadOnlyCore,
	request api.Request,
	paths []string,
) error {
	return runCLIUploadOnlyQueue(ctx, coreSvc, newCLIPreparationBatch(request, paths), false, api.NopLogger{})
}

func TestCLIPreparationBatchOwnsPerSourceInstructions(t *testing.T) {
	t.Parallel()

	tmdbID := 1234567
	defaults := api.Request{
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID},
		PlaylistInstruction: api.PlaylistInstruction{Set: true, Selected: []string{"00001.mpls"}},
	}
	batch := newCLIPreparationBatch(defaults, []string{"one", "two"})
	*defaults.ExternalIDOverrides.TMDBID = 7654321
	defaults.PlaylistInstruction.Selected[0] = "99999.mpls"
	*batch.items[0].externalIDs.TMDBID = 1111111
	batch.items[0].playlistInstruction.Selected[0] = "00002.mpls"

	if got := *batch.items[1].externalIDs.TMDBID; got != 1234567 {
		t.Fatalf("second item TMDB ID = %d, want detached value", got)
	}
	if got := batch.items[1].playlistInstruction.Selected[0]; got != "00001.mpls" {
		t.Fatalf("second item playlist = %q, want detached value", got)
	}
}

func runCLIUploadOnlyBatchForPaths(
	ctx context.Context,
	coreSvc cliUploadOnlyCore,
	request api.Request,
	paths []string,
	debug bool,
	logger api.Logger,
) error {
	return runCLIUploadOnlyBatch(ctx, coreSvc, newCLIPreparationBatch(request, paths), debug, logger)
}

func handleBDMVPlaylistSelectionForPaths(
	ctx context.Context,
	paths []string,
	coreSvc cliPlaylistSelectionCore,
	cfg config.Config,
	opts cliOptions,
) error {
	batch := newCLIPreparationBatch(api.Request{}, paths)
	return handleBDMVPlaylistSelection(ctx, &batch, coreSvc, cfg, api.NopLogger{}, opts)
}

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
	err := runInteractiveCLIPathWithInput(ctx, coreSvc, cliOptions{}, map[string]bool{}, "movie.mkv", 0, config.Config{}, strings.NewReader(""))
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

	persisted := servePersistConfig(stored, runtime, map[string]bool{
		"persist-listen": true,
		"addr":           true,
		"base-url":       true,
	})
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
	stored := webserver.CLIConfig{
		Host:    "localhost",
		Port:    7480,
		BaseURL: "/stored/",
	}
	runtime := stored
	runtime.Host = "0.0.0.0"
	runtime.Port = 9090
	runtime.BaseURL = "/explicit/"

	persisted := servePersistConfig(stored, runtime, map[string]bool{
		"persist-listen":     true,
		"persist-web-config": true,
		"addr":               true,
		"base-url":           true,
	})
	if persisted.Host != "0.0.0.0" || persisted.Port != 9090 || persisted.BaseURL != "/explicit/" {
		t.Fatalf("explicit web config not persisted: %#v", persisted)
	}
}

func TestServePersistConfigListenOnlyPersistsExplicitListenFields(t *testing.T) {
	stored := webserver.CLIConfig{
		Host:    "localhost",
		Port:    7480,
		BaseURL: "/stored/",
	}
	runtime := webserver.CLIConfig{
		Host:    "0.0.0.0",
		Port:    9090,
		BaseURL: "/env/",
	}

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "port": true})
	if persisted.Host != "localhost" || persisted.Port != 9090 || persisted.BaseURL != "" {
		t.Fatalf("listen persistence included env/transient fields: %#v", persisted)
	}
}

func TestServePersistConfigListenOnlyIgnoresInvalidStoredBaseURLWhenSaving(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	stored := webserver.CLIConfig{
		Host:    "localhost",
		Port:    7480,
		BaseURL: "javascript:alert(1)",
	}
	runtime := webserver.CLIConfig{
		Host:    "127.0.0.1",
		Port:    9090,
		BaseURL: "javascript:alert(1)",
	}

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
	distPath := filepath.Join(tmpDir, "webui", "dist")
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

func TestPrepareCLIUploadMetadataKeepsExternalOverridesAfterResolvedPath(t *testing.T) {
	t.Parallel()

	sourcePath := "folder"
	resolvedPath := filepath.Join("folder", "movie.mkv")
	tmdbID := 12345
	malID := 67890

	coreSvc := &cliCoreForTest{
		previewResponses: []api.MetadataPreview{
			{SourcePath: resolvedPath},
			{SourcePath: resolvedPath},
		},
	}
	req := api.Request{
		SourcePath:          sourcePath,
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID, MALID: &malID},
	}

	resolvedReq, err := prepareCLIUploadMetadata(context.Background(), coreSvc, req)
	if err != nil {
		t.Fatalf("prepareCLIUploadMetadata: %v", err)
	}
	if len(coreSvc.requests) != 2 {
		t.Fatalf("expected 2 preview requests, got %#v", coreSvc.requests)
	}
	if coreSvc.requests[0].req.SourcePath != sourcePath {
		t.Fatalf("expected first preview for source path, got %q", coreSvc.requests[0].req.SourcePath)
	}
	if coreSvc.requests[1].req.SourcePath != resolvedPath {
		t.Fatalf("expected second preview for resolved path, got %q", coreSvc.requests[1].req.SourcePath)
	}
	if resolvedReq.SourcePath != resolvedPath {
		t.Fatalf("expected resolved upload path, got %q", resolvedReq.SourcePath)
	}
	if resolvedReq.ExternalIDOverrides.TMDBID == nil || *resolvedReq.ExternalIDOverrides.TMDBID != tmdbID {
		t.Fatalf("expected external TMDB override to remain source-local, got %#v", resolvedReq.ExternalIDOverrides)
	}
	if resolvedReq.ExternalIDOverrides.MALID == nil || *resolvedReq.ExternalIDOverrides.MALID != malID {
		t.Fatalf("expected MAL override to remain source-local, got %#v", resolvedReq.ExternalIDOverrides)
	}
	if second := coreSvc.requests[1].req.ExternalIDOverrides; second.TMDBID == nil || *second.TMDBID != tmdbID || second.MALID == nil || *second.MALID != malID {
		t.Fatalf("expected second preview to retain source-local overrides, got %#v", second)
	}
}

func TestCLIPreparationBatchKeepsSourceChoicesIndependent(t *testing.T) {
	t.Parallel()

	firstID, secondID := 12345, 99999
	batch := newCLIPreparationBatch(api.Request{}, []string{"one.mkv", "two.mkv"})
	batch.items[0].externalIDs.TMDBID = &firstID
	batch.items[1].externalIDs.TMDBID = &secondID
	first, second := batch.request(0), batch.request(1)
	if *first.ExternalIDOverrides.TMDBID != firstID || *second.ExternalIDOverrides.TMDBID != secondID {
		t.Fatalf("source choices crossed batch items: first=%#v second=%#v", first.ExternalIDOverrides, second.ExternalIDOverrides)
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
			if req.SourcePath != "" {
				uploadPaths = append(uploadPaths, req.SourcePath)
				if req.SourcePath == failOn {
					return api.Result{}, errors.New("upload boom")
				}
			}
			return api.Result{UploadedCount: 1}, nil
		},
	}
	paths := []string{"good1", failOn, "good2"}
	err := runCLIUploadOnlyQueueForPaths(context.Background(), coreSvc, api.Request{}, paths)
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
	err := runCLIUploadOnlyQueueForPaths(context.Background(), coreSvc, api.Request{}, paths)
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
		if strings.TrimSpace(r.req.SourcePath) == "" {
			t.Fatal("expected nonblank single-source upload request per item")
		}
	}
}

func TestRunCLIUploadOnlyQueueCapturesOnlyDVDItems(t *testing.T) {
	t.Parallel()

	dvdRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(dvdRoot, "VIDEO_TS"), 0o700); err != nil {
		t.Fatalf("create VIDEO_TS: %v", err)
	}
	nonDiscRoot := t.TempDir()
	coreSvc := &cliCoreForTest{dvdMenuResult: api.DVDMenuCaptureResult{
		Images: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{Path: "dvd-menu.png", Purpose: api.ScreenshotPurposeMenu}}},
	}}
	err := runCLIUploadOnlyQueueForPaths(context.Background(), coreSvc, api.Request{
		Options: api.UploadOptions{CaptureDVDMenus: true},
	}, []string{dvdRoot, nonDiscRoot})
	if err != nil {
		t.Fatalf("upload-only queue: %v", err)
	}
	if coreSvc.dvdMenuCaptureCalls != 1 {
		t.Fatalf("capture calls = %d, want 1", coreSvc.dvdMenuCaptureCalls)
	}
	if coreSvc.runUploadPreparedCalls != 2 {
		t.Fatalf("upload calls = %d, want 2", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunCLIUploadOnlyQueueDVDMenuCaptureFailureStopsUpload(t *testing.T) {
	t.Parallel()

	dvdRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(dvdRoot, "VIDEO_TS"), 0o700); err != nil {
		t.Fatalf("create VIDEO_TS: %v", err)
	}
	coreSvc := &cliCoreForTest{dvdMenuErr: errSyntheticDVDMenuCapture}
	err := runCLIUploadOnlyQueueForPaths(context.Background(), coreSvc, api.Request{
		Options: api.UploadOptions{CaptureDVDMenus: true},
	}, []string{dvdRoot})

	assertDVDMenuCaptureError(t, err)
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,capture-dvd-menus" {
		t.Fatalf("queue capture failure call order = %s", got)
	}
	if coreSvc.runUploadPreparedCalls != 0 {
		t.Fatalf("queue upload calls after capture failure = %d, want 0", coreSvc.runUploadPreparedCalls)
	}
}

func TestRunCLIUploadOnlyQueueRecordsEachEmptyDVDMenuCapture(t *testing.T) {
	t.Parallel()

	paths := []string{t.TempDir(), t.TempDir()}
	for _, path := range paths {
		if err := os.Mkdir(filepath.Join(path, "VIDEO_TS"), 0o700); err != nil {
			t.Fatalf("create VIDEO_TS: %v", err)
		}
	}
	coreSvc := &cliCoreForTest{}
	err := runCLIUploadOnlyQueueForPaths(context.Background(), coreSvc, api.Request{
		Options: api.UploadOptions{CaptureDVDMenus: true},
	}, paths)
	if err == nil {
		t.Fatal("expected queue summary error for empty DVD menu captures")
	}
	if coreSvc.dvdMenuCaptureCalls != len(paths) {
		t.Fatalf("capture calls = %d, want %d", coreSvc.dvdMenuCaptureCalls, len(paths))
	}
	if coreSvc.runUploadPreparedCalls != 0 {
		t.Fatalf("upload calls = %d, want 0", coreSvc.runUploadPreparedCalls)
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
	if err := runCLIUploadOnlyBatchForPaths(context.Background(), coreSvc, api.Request{}, paths, false, api.NopLogger{}); err != nil {
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
	if coreSvc.runUploadPreparedCalls != len(paths) {
		t.Fatalf("expected one independent RunUploadPrepared call per source, got %d", coreSvc.runUploadPreparedCalls)
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
	if err := runCLIUploadOnlyBatchForPaths(context.Background(), coreSvc, api.Request{}, paths, false, api.NopLogger{}); err != nil {
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

func TestRunCLIUploadOnlyBatchCapturesBeforeDebugReviews(t *testing.T) {
	t.Parallel()

	dvdRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(dvdRoot, "VIDEO_TS"), 0o700); err != nil {
		t.Fatalf("create VIDEO_TS: %v", err)
	}
	coreSvc := &cliCoreForTest{
		dvdMenuResult: api.DVDMenuCaptureResult{
			Images: []api.DVDMenuCaptureImage{{ScreenshotImage: api.ScreenshotImage{Path: "dvd-menu.png", Purpose: api.ScreenshotPurposeMenu}}},
		},
		review: api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "BLU"}}},
	}
	paths := []string{dvdRoot}
	if err := runCLIUploadOnlyBatchForPaths(context.Background(), coreSvc, api.Request{
		Options: api.UploadOptions{CaptureDVDMenus: true},
	}, paths, true, api.NopLogger{}); err != nil {
		t.Fatalf("upload-only batch: %v", err)
	}
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,capture-dvd-menus,review" {
		t.Fatalf("batch call order = %s", got)
	}
}

func TestRunCLIUploadOnlyBatchContinuesAfterEmptyDVDMenuCapture(t *testing.T) {
	t.Parallel()

	paths := []string{t.TempDir(), t.TempDir()}
	for _, path := range paths {
		if err := os.Mkdir(filepath.Join(path, "VIDEO_TS"), 0o700); err != nil {
			t.Fatalf("create VIDEO_TS: %v", err)
		}
	}
	coreSvc := &cliCoreForTest{}
	logger := &cliErrorCountingLogger{}
	if err := runCLIUploadOnlyBatchForPaths(context.Background(), coreSvc, api.Request{
		Options: api.UploadOptions{CaptureDVDMenus: true},
	}, paths, false, logger); err != nil {
		t.Fatalf("upload-only batch: %v", err)
	}
	if coreSvc.dvdMenuCaptureCalls != len(paths) {
		t.Fatalf("capture calls = %d, want %d", coreSvc.dvdMenuCaptureCalls, len(paths))
	}
	if logger.errors != len(paths) {
		t.Fatalf("recorded capture failures = %d, want %d", logger.errors, len(paths))
	}
	if coreSvc.runUploadPreparedCalls != len(paths) {
		t.Fatalf("upload calls = %d, want %d", coreSvc.runUploadPreparedCalls, len(paths))
	}
}

func TestRunCLIUploadOnlyBatchDVDMenuCaptureFailureStopsReviewAndUpload(t *testing.T) {
	t.Parallel()

	dvdRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(dvdRoot, "VIDEO_TS"), 0o700); err != nil {
		t.Fatalf("create VIDEO_TS: %v", err)
	}
	coreSvc := &cliCoreForTest{dvdMenuErr: errSyntheticDVDMenuCapture}
	paths := []string{dvdRoot}
	err := runCLIUploadOnlyBatchForPaths(context.Background(), coreSvc, api.Request{
		Options: api.UploadOptions{CaptureDVDMenus: true},
	}, paths, true, api.NopLogger{})

	assertDVDMenuCaptureError(t, err)
	if got := strings.Join(coreSvc.callOrder, ","); got != "preview,capture-dvd-menus" {
		t.Fatalf("batch capture failure call order = %s", got)
	}
	if coreSvc.runUploadPreparedCalls != 0 {
		t.Fatalf("batch upload calls after capture failure = %d, want 0", coreSvc.runUploadPreparedCalls)
	}
}

type cliErrorCountingLogger struct {
	api.NopLogger
	errors int
}

func (l *cliErrorCountingLogger) Errorf(string, ...any) {
	l.errors++
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
	err := handleBDMVPlaylistSelectionForPaths(ctx, []string{root}, &cliCoreForTest{}, config.Config{}, cliOptions{})
	if err == nil || !isCtxErr(err) {
		t.Fatalf("expected a context error from DetectDiscType, got %v", err)
	}
}

func TestHandleBDMVPlaylistSelectionReturnsOnLoadSelectionCtxErr(t *testing.T) {
	t.Parallel()

	root := newBDMVTempDir(t)
	coreSvc := &cliCoreForTest{playlistSelectionErr: context.DeadlineExceeded}
	err := handleBDMVPlaylistSelectionForPaths(context.Background(), []string{root}, coreSvc, config.Config{}, cliOptions{})
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
		err := handleBDMVPlaylistSelectionForPaths(context.Background(), []string{root}, coreSvc, config.Config{
			Metadata: config.MetadataConfig{UseLargestPlaylist: useLargest},
		}, cliOptions{})
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
		err := handleBDMVPlaylistSelectionForPaths(context.Background(), []string{root}, coreSvc, config.Config{
			Metadata: config.MetadataConfig{UseLargestPlaylist: useLargest},
		}, cliOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("UseLargestPlaylist=%v: expected Canceled from SavePlaylistSelection, got %v", useLargest, err)
		}
	}
}

func TestHandleBDMVPlaylistSelectionSkipsNonCtxErrors(t *testing.T) {
	t.Parallel()

	root := newBDMVTempDir(t)
	coreSvc := &cliCoreForTest{playlistSelectionErr: errors.New("boom")}
	err := handleBDMVPlaylistSelectionForPaths(context.Background(), []string{root}, coreSvc, config.Config{}, cliOptions{})
	if err != nil {
		t.Fatalf("expected non-ctx load error to be skipped (continue), got %v", err)
	}
}

type perSourcePlaylistCore struct {
	playlists map[string][]api.PlaylistInfo
}

func (*perSourcePlaylistCore) DetectDiscType(ctx context.Context, sourcePath string) (string, error) {
	layout, err := sourcelayout.Resolve(ctx, sourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve source layout: %w", err)
	}
	return layout.DiscType, nil
}

func (c *perSourcePlaylistCore) DiscoverPlaylists(_ context.Context, sourcePath string) ([]api.PlaylistInfo, error) {
	return append([]api.PlaylistInfo(nil), c.playlists[filepath.Clean(sourcePath)]...), nil
}

func (*perSourcePlaylistCore) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return nil
}

func (*perSourcePlaylistCore) LoadPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, internalerrors.ErrNotFound
}

func TestCLITwoBDMVSourcesKeepIndependentPlaylistInstructions(t *testing.T) {
	t.Parallel()

	paths := []string{filepath.Join(t.TempDir(), "disc-one"), filepath.Join(t.TempDir(), "disc-two")}
	for _, sourcePath := range paths {
		if err := os.MkdirAll(filepath.Join(sourcePath, "BDMV"), 0o700); err != nil {
			t.Fatalf("create BDMV source: %v", err)
		}
	}
	selectionCore := &perSourcePlaylistCore{playlists: map[string][]api.PlaylistInfo{
		filepath.Clean(paths[0]): {{File: "00001.mpls", Score: 2}},
		filepath.Clean(paths[1]): {{File: "00002.mpls", Score: 2}},
	}}
	batch := newCLIPreparationBatch(api.Request{}, paths)
	if err := handleBDMVPlaylistSelection(
		context.Background(),
		&batch,
		selectionCore,
		config.Config{Metadata: config.MetadataConfig{UseLargestPlaylist: true}},
		api.NopLogger{},
		cliOptions{Unattended: true},
	); err != nil {
		t.Fatalf("resolve playlist selections: %v", err)
	}
	if got := batch.items[0].playlistInstruction; !got.Set || !slices.Equal(got.Selected, []string{"00001.mpls"}) {
		t.Fatalf("first instruction = %#v", got)
	}
	if got := batch.items[1].playlistInstruction; !got.Set || !slices.Equal(got.Selected, []string{"00002.mpls"}) {
		t.Fatalf("second instruction = %#v", got)
	}

	workflowCore := &cliCoreForTest{}
	if err := runCLIUploadOnlyBatch(context.Background(), workflowCore, batch, false, api.NopLogger{}); err != nil {
		t.Fatalf("run upload-only batch: %v", err)
	}
	var uploads []api.Request
	for _, recorded := range workflowCore.requests {
		if recorded.name == "upload" {
			uploads = append(uploads, recorded.req)
		}
	}
	if len(uploads) != 2 {
		t.Fatalf("upload requests = %d, want 2", len(uploads))
	}
	for index := range uploads {
		if uploads[index].SourcePath != paths[index] ||
			!slices.Equal(uploads[index].PlaylistInstruction.Selected, batch.items[index].playlistInstruction.Selected) {
			t.Fatalf("upload %d = %#v", index, uploads[index])
		}
	}
}
