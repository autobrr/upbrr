// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/pathing"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

const liveTestJournalName = "image-effects.private.jsonl"
const liveTestCleanupMarker = "cleanup-started"

func newLiveTestCommand(streams cliIO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "live-test",
		Short: "Manage isolated live-test profiles",
		Args:  cobra.NoArgs,
	}
	cmd.SetIn(streams.in)
	cmd.SetOut(streams.out)
	cmd.SetErr(streams.errOut)
	var runDir, sourceConfig string
	var preferDeletableHosts bool
	initCmd := &cobra.Command{
		Use:   "init --run-dir <path>",
		Short: "Snapshot configured credentials into an isolated test profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := livetest.ValidateRunDir(runDir); err != nil {
				return fmt.Errorf("live-test init: %w", err)
			}
			lock, err := livetest.Lock()
			if err != nil {
				return fmt.Errorf("live-test init lock: %w", err)
			}
			defer lock.Close()
			options := configstore.LiveTestProfileOptions{}
			if preferDeletableHosts {
				registry, err := trackerimpl.NewRegistry()
				if err != nil {
					return fmt.Errorf("live-test init tracker registry: %w", err)
				}
				options.PreferDeletableHosts = true
				options.Registry = registry
			}
			profile, err := configstore.CreateLiveTestProfileWithOptions(
				cmd.Context(), sourceConfig, cmd.Flags().Changed("config"), runDir, options,
			)
			if err != nil {
				return fmt.Errorf("live-test init: %w", err)
			}
			data, err := json.Marshal(profile)
			if err != nil {
				return fmt.Errorf("live-test encode profile receipt: %w", err)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(data)); err != nil {
				return fmt.Errorf("live-test write profile receipt: %w", err)
			}
			return nil
		},
	}
	initCmd.Flags().StringVar(&runDir, "run-dir", "", "New directory under the private live-test runs root")
	initCmd.Flags().StringVar(&sourceConfig, "config", "", "Source configuration using normal config precedence")
	initCmd.Flags().BoolVar(&preferDeletableHosts, "prefer-deletable-hosts", false, "Prefer configured deletable image hosts in the isolated profile")
	var cleanupDir string
	cleanupCmd := &cobra.Command{
		Use:   "cleanup --run-dir <path>",
		Short: "Delete only journaled images owned by this run",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cleanupLiveTestRun(cmd, cleanupDir) },
	}
	cleanupCmd.Flags().StringVar(&cleanupDir, "run-dir", "", "Existing isolated live-test run directory")
	cmd.AddCommand(initCmd, cleanupCmd)
	return cmd
}

// openLiveTestRuntime validates the manifest before normal config bootstrap can
// write anything, and retains the process lock through all background shutdown.
func openLiveTestRuntime(ctx context.Context, configPath string, provided bool, maxImages int) (config.Config, *api.LiveTestPolicy, *os.File, error) {
	if maxImages < 0 || maxImages > api.MaxLiveTestImageUploads {
		return config.Config{}, nil, nil, fmt.Errorf("live-test image limit must be between 0 and %d", api.MaxLiveTestImageUploads)
	}
	if !provided || strings.TrimSpace(configPath) == "" {
		return config.Config{}, nil, nil, errors.New("--live-test requires --config from a ready live-test profile")
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(entry), "UPBRR_E2E_") {
			return config.Config{}, nil, nil, errors.New("live-test rejects UPBRR_E2E_* environment substitutions")
		}
	}
	path, err := filepath.Abs(configPath)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("live-test config path: %w", err)
	}
	profile, err := livetest.LoadProfile(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("live-test runtime profile: %w", err)
	}
	if !pathing.SamePath(path, profile.ConfigPath) {
		return config.Config{}, nil, nil, errors.New("live-test config does not match the ready profile")
	}
	lock, err := livetest.Lock()
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("live-test runtime lock: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
		}
	}()
	if _, err := os.Lstat(filepath.Join(profile.RunDir, liveTestCleanupMarker)); !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, nil, nil, errors.New("live-test cleanup has started; use a new run for further preparation")
	}
	if err := bindLiveTestImageLimit(profile, maxImages); err != nil {
		return config.Config{}, nil, nil, err
	}
	cfg, err := configstore.LoadLiveTestConfig(ctx, profile)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("live-test load isolated config: %w", err)
	}
	policy, err := api.NewLiveTestPolicy(profile.RunID, filepath.Join(profile.RunDir, liveTestJournalName), maxImages)
	if err != nil {
		return config.Config{}, nil, nil, fmt.Errorf("live-test policy: %w", err)
	}
	success = true
	return *cfg, policy, lock, nil
}

func cleanupLiveTestRun(cmd *cobra.Command, runDir string) error {
	profile, err := livetest.LoadProfile(runDir)
	if err != nil {
		return fmt.Errorf("live-test cleanup profile: %w", err)
	}
	lock, err := livetest.Lock()
	if err != nil {
		return fmt.Errorf("live-test cleanup lock: %w", err)
	}
	defer lock.Close()
	// Cleanup is terminal for preparation. Unknown uploads remain unresolved;
	// unconfirmed deletions are reported as retained and are never retried.
	runRoot, err := os.OpenRoot(profile.RunDir)
	if err != nil {
		return fmt.Errorf("live-test cleanup directory: %w", err)
	}
	defer runRoot.Close()
	marker, err := runRoot.OpenFile(liveTestCleanupMarker, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("live-test cleanup marker: %w", err)
	}
	err = marker.Sync()
	closeErr := marker.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("live-test persist cleanup marker: %w", errors.Join(err, closeErr))
	}
	// A fresh bounded context permits cleanup after the main workflow cancels.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), 2*time.Minute)
	defer cancel()
	cfg, err := configstore.LoadLiveTestConfig(ctx, profile)
	if err != nil {
		return fmt.Errorf("live-test cleanup config: %w", err)
	}
	report := imagehosting.CleanupReport{RunID: profile.RunID, Images: []imagehosting.CleanupImageResult{}}
	var cleanupErr error
	if _, err := runRoot.Lstat(liveTestJournalName); !errors.Is(err, os.ErrNotExist) {
		report, cleanupErr = imagehosting.CleanupLiveTestImages(ctx, *cfg, profile.RunID, filepath.Join(profile.RunDir, liveTestJournalName))
	}
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("live-test cleanup receipt: %w", err)
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(data)); err != nil {
		return fmt.Errorf("live-test write cleanup receipt: %w", err)
	}
	if cleanupErr != nil {
		return fmt.Errorf("live-test cleanup: %w", cleanupErr)
	}
	if report.Pending != 0 || report.Unknown != 0 || report.Failed != 0 {
		return errors.New("live-test cleanup has unresolved effects; preserve the run and reconcile its private journal")
	}
	return nil
}

func bindLiveTestImageLimit(profile livetest.Profile, maxImages int) error {
	const filename = "runtime-policy.json"
	type runtimePolicy struct {
		RunID     string `json:"runId"`
		MaxImages int    `json:"maxImages"`
	}
	expected := runtimePolicy{RunID: profile.RunID, MaxImages: maxImages}
	root, err := os.OpenRoot(profile.RunDir)
	if err != nil {
		return fmt.Errorf("live-test policy directory: %w", err)
	}
	defer root.Close()
	file, err := root.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, err := root.Open(filename)
		if err != nil {
			return fmt.Errorf("live-test read retained policy: %w", err)
		}
		defer existing.Close()
		var retained runtimePolicy
		if err := json.UnmarshalRead(io.LimitReader(existing, 4096), &retained, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("live-test decode retained policy: %w", err)
		}
		if retained != expected {
			return errors.New("live-test image budget differs from this run's retained policy; create a new run")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("live-test create retained policy: %w", err)
	}
	defer file.Close()
	if err := json.MarshalWrite(file, expected); err != nil {
		return fmt.Errorf("live-test write retained policy: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("live-test sync retained policy: %w", err)
	}
	return nil
}
