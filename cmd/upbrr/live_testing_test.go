// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLiveTestCLIRejectsUnisolatedAndConflictingStartup(t *testing.T) {
	for _, args := range [][]string{
		{"--live-test", "--create-auth"},
		{"--live-test", "--cleanup"},
		{"--live-test-max-images", "1", "synthetic.mkv"},
		{"--live-test", "synthetic.mkv"},
		{"serve", "--live-test"},
		{"serve", "--live-test-max-images", "1"},
		{"live-test", "cleanup", "--run-dir", t.TempDir()},
	} {
		result := executeCLIForTest(t.Context(), t, args)
		if result.code == 0 || result.err == nil {
			t.Fatalf("unsafe CLI accepted: %v", args)
		}
	}
}

func TestLiveTestCLIProfileStartupAndTerminalCleanup(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("XDG_CACHE_HOME", base)
	t.Setenv("HOME", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "source"))
	t.Setenv("UA_DEFAULT_DB_PATH", "")
	t.Setenv("UA_TRACKERS_DEFAULT", "")
	source, err := db.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	authfixture.Write(t, source)
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = source
	cfg.Trackers.DefaultTrackers = config.CSVList{"LST"}
	if err := configstore.SaveToDBPath(t.Context(), cfg, source); err != nil {
		t.Fatal(err)
	}
	root, err := livetest.PrivateRoot()
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "runs", "synthetic-cli")
	result := executeCLIForTest(t.Context(), t, []string{"live-test", "init", "--run-dir", runDir})
	if result.err != nil {
		t.Fatal(result.err)
	}
	var profile livetest.Profile
	if err := json.Unmarshal([]byte(result.stdout), &profile); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UA_DEFAULT_DB_PATH", source)
	loaded, policy, lock, err := openLiveTestRuntime(t.Context(), profile.ConfigPath, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MainSettings.DBPath != profile.DBPath || policy.RunID() != profile.RunID {
		t.Fatal("runtime escaped its ready profile")
	}
	if _, _, second, err := openLiveTestRuntime(t.Context(), profile.ConfigPath, true, 0); err == nil {
		_ = second.Close()
		t.Fatal("concurrent runtime was accepted")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, unsafeLock, err := openLiveTestRuntime(t.Context(), profile.ConfigPath, true, 1); err == nil {
		_ = unsafeLock.Close()
		t.Fatal("restart increased the run's image budget")
	}
	t.Setenv("UPBRR_E2E_TEST", "1")
	if _, _, unsafeLock, err := openLiveTestRuntime(t.Context(), profile.ConfigPath, true, 0); err == nil {
		_ = unsafeLock.Close()
		t.Fatal("E2E substitution accepted")
	}
	t.Setenv("UPBRR_E2E_TEST", "")
	if err := os.Unsetenv("UPBRR_E2E_TEST"); err != nil {
		t.Fatal(err)
	}
	// A startup that never reached image service construction still has no effects.
	for range 2 {
		result = executeCLIForTest(t.Context(), t, []string{"live-test", "cleanup", "--run-dir", runDir})
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	if _, _, unsafeLock, err := openLiveTestRuntime(t.Context(), profile.ConfigPath, true, 0); err == nil {
		_ = unsafeLock.Close()
		t.Fatal("preparation resumed after terminal cleanup")
	}
}

func TestLiveTestCLIUsesSafeCompositeWithoutDebug(t *testing.T) {
	var output strings.Builder
	coreSvc := &cliWorkflowCoreFake{
		liveTest: true,
		current: releaseworkflow.CommandResult{
			DryRun: &api.UploadDryRunResult{Status: api.StageStatusCompleted, NoSeed: true},
		},
	}
	session := &cliWorkflowSession{
		core:   coreSvc,
		intent: cliWorkflowIntent{noSeed: true, interaction: api.InteractionModeUnattended},
		uploadRequest: api.Request{
			SourcePath: filepath.Join(t.TempDir(), "Synthetic.Release.2026.1080p-GRP.mkv"),
			Options:    api.UploadOptions{NoSeed: true, InteractionMode: api.InteractionModeUnattended},
		},
		streams: cliIO{out: &output},
	}
	if _, err := session.completeComposite(t.Context(), false, bufio.NewReader(strings.NewReader("")), config.Config{}, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	if coreSvc.liveStarts != 1 || len(coreSvc.uploadRequests) != 1 {
		t.Fatal("CLI did not use the authorized safe composite path")
	}
	if !strings.Contains(output.String(), "Live testing:") || strings.Contains(output.String(), "Debug mode:") {
		t.Fatal("live-test output mislabeled normal rules as debug")
	}
	if coreSvc.uploadRequests[0].Execution.Mode == api.ReleaseWorkflowUploadModeDebug {
		t.Fatal("live-test CLI changed execution rules")
	}
}
