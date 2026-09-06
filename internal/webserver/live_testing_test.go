// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/authmaterial/authfixture"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLiveTestHTTPFailureAndApplicationInfo(t *testing.T) {
	t.Parallel()
	policy, err := api.NewLiveTestPolicy("web-run", filepath.Join(t.TempDir(), "private-images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	writeAppError(recorder, fmt.Errorf("adapter: %w", policy.RejectRequest(api.OperationKindUploadExecute)))
	var body struct {
		Error   string               `json:"error"`
		Failure api.OperationFailure `json:"failure"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusForbidden || body.Failure.Code != api.OperationFailureLiveTestMutationDisabled ||
		body.Failure.Operation != api.OperationKindUploadExecute || body.Failure.Recovery != api.OperationRecoveryNone ||
		body.Error == "" || body.Error != body.Failure.Message {
		t.Fatalf("HTTP response = %d %#v", recorder.Code, body)
	}
	for _, liveTest := range []*api.LiveTestPolicy{nil, policy} {
		backend := &Backend{liveTest: liveTest}
		info, err := backend.GetApplicationInfo()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		_, present := fields["testRuntime"]
		if liveTest == nil {
			if info.TestRuntime != nil || present {
				t.Fatalf("ordinary application info published live-test capability: %s", encoded)
			}
		} else if !present || info.TestRuntime == nil || info.TestRuntime.RunID != "web-run" || info.TestRuntime.TrackerSubmission.RequestsDenied != 1 {
			t.Fatalf("live application info = %#v", info.TestRuntime)
		}
	}
}

func TestLiveTestRuntimeGenerationPreservesDenialPolicy(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("XDG_CACHE_HOME", base)
	t.Setenv("HOME", base)
	t.Setenv("UA_DEFAULT_SCREENS", "9")
	root, err := livetest.PrivateRoot()
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "runs", "runtime-run")
	profileDir := filepath.Join(runDir, "profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := livetest.Profile{
		Version:         1,
		RunID:           "runtime-run",
		RunDir:          runDir,
		DBPath:          filepath.Join(profileDir, "db.sqlite"),
		ConfigPath:      filepath.Join(profileDir, "config.yaml"),
		DefaultTrackers: []string{"LST"},
	}
	if err := os.WriteFile(profile.ConfigPath, []byte("synthetic profile"), 0o600); err != nil {
		t.Fatal(err)
	}
	authfixture.Write(t, profile.DBPath)
	repo, err := db.OpenWithLogger(profile.DBPath, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if err := livetest.PublishProfile(profile); err != nil {
		t.Fatal(err)
	}
	repo, err = db.OpenWithLogger(profile.DBPath, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	policy, err := api.NewLiveTestPolicy(profile.RunID, filepath.Join(runDir, "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := validRuntimeActivationConfig()
	cfg.ImageHosting.LostimgAPI = "synthetic-original-image-key"
	cfg.Trackers.Trackers = map[string]config.TrackerConfig{
		"HDB": {
			Username:  "synthetic-original-user",
			Passkey:   "synthetic-original-passkey",
			ImgRehost: true,
		},
		"PTP": {ImageHost: "imgbox"},
		"THR": {ImgAPI: "synthetic-original-thr-key"},
	}
	backend := &Backend{
		repo:     repo,
		liveTest: policy,
		cfg:      cfg,
	}
	activator, err := backend.runtimeActivator()
	if err != nil {
		t.Fatal(err)
	}
	installer := &activationTestInstaller{}
	activator.installer = installer
	activator.deps.cookies = activationTestCookies
	var stored config.Config
	activator.deps.persist = func(ctx context.Context, cfg *config.Config, repo *db.SQLiteRepository, dbPath string, _ api.Logger) error {
		stored = *cfg
		return configstore.SaveToRepository(ctx, cfg, repo, dbPath)
	}
	cfg.MainSettings.DBPath = filepath.Join(base, "outside.db")
	cfg.TorrentClients = map[string]config.TorrentClientConfig{"watch": {Type: "watch", WatchFolder: filepath.Join(base, "outside-watch")}}
	for range 2 {
		if err := activator.Activate(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		generation := installer.generations[len(installer.generations)-1]
		_, err = generation.Capabilities.ReleaseWorkflow.StartReleaseWorkflowUpload(t.Context(), "owner", api.CreateReleaseWorkflowUploadRequest{
			Execution: api.ReleaseWorkflowUploadExecution{Mode: api.ReleaseWorkflowUploadModeUpload},
		})
		RetiredRuntime{Owner: generation.Owner, Logger: generation.Logger}.Close()
		if !errors.Is(err, api.ErrLiveTestMutationDisabled) {
			t.Fatalf("replacement runtime submission = %v", err)
		}
		if generation.Config.MainSettings.DBPath != profile.DBPath || stored.MainSettings.DBPath != profile.DBPath ||
			generation.Config.TorrentClients["watch"].WatchFolder != filepath.Join(runDir, "artifacts", "watch") ||
			generation.Config.ScreenshotHandling.Screens != 1 || stored.ScreenshotHandling.Screens != 1 {
			t.Fatal("config activation escaped profile paths or reapplied environment overrides")
		}
	}
	if got := policy.Snapshot().TrackerSubmission; got != (api.LiveTestEffectCounts{RequestsDenied: 2}) {
		t.Fatalf("shared generation receipt = %#v", got)
	}
	for _, mutate := range []func(*config.ImageHostingConfig){
		func(imageHosting *config.ImageHostingConfig) {
			imageHosting.LostimgAPI = "synthetic-different-image-key"
		},
		func(imageHosting *config.ImageHostingConfig) { imageHosting.LostimgAPI = "" },
		func(imageHosting *config.ImageHostingConfig) {
			imageHosting.ShareXURL = "https://images.invalid/upload"
		},
	} {
		candidate := cfg
		mutate(&candidate.ImageHosting)
		err := activator.Activate(t.Context(), candidate)
		assertActivationStage(t, err, ActivationStageValidateStored)
		if len(installer.generations) != 2 {
			t.Fatal("credential change reached runtime installation")
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{
			name: "HDB uploader credentials",
			mutate: func(candidate *config.Config) {
				hdb := candidate.Trackers.Trackers["HDB"]
				hdb.Username = "synthetic-different-user"
				hdb.Passkey = "synthetic-different-passkey"
				candidate.Trackers.Trackers["HDB"] = hdb
			},
		},
		{
			name: "THR uploader credential",
			mutate: func(candidate *config.Config) {
				thr := candidate.Trackers.Trackers["THR"]
				thr.ImgAPI = "synthetic-different-thr-key"
				candidate.Trackers.Trackers["THR"] = thr
			},
		},
		{
			name: "tracker image host",
			mutate: func(candidate *config.Config) {
				ptp := candidate.Trackers.Trackers["PTP"]
				ptp.ImageHost = "pixhost"
				candidate.Trackers.Trackers["PTP"] = ptp
			},
		},
		{
			name: "tracker rehost policy",
			mutate: func(candidate *config.Config) {
				hdb := candidate.Trackers.Trackers["HDB"]
				hdb.ImgRehost = false
				candidate.Trackers.Trackers["HDB"] = hdb
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := cloneConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(candidate)
			err = activator.Activate(t.Context(), *candidate)
			assertActivationStage(t, err, ActivationStageValidateStored)
			if len(installer.generations) != 2 {
				t.Fatal("tracker image-host change reached runtime installation")
			}
		})
	}
	duplicate, err := cloneConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	duplicate.Trackers.Trackers["hdb"] = config.TrackerConfig{
		Username: "synthetic-alias-user",
		Passkey:  "synthetic-alias-passkey",
	}
	err = activator.Activate(t.Context(), *duplicate)
	assertActivationStage(t, err, ActivationStageValidateStored)
	if !strings.Contains(err.Error(), "duplicate case-insensitive names") || len(installer.generations) != 2 {
		t.Fatalf("ambiguous tracker activation = %v, generations = %d", err, len(installer.generations))
	}
	unrelated, err := cloneConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hdb := unrelated.Trackers.Trackers["HDB"]
	hdb.Channel = "synthetic-unrelated-channel"
	unrelated.Trackers.Trackers["HDB"] = hdb
	if err := activator.Activate(t.Context(), *unrelated); err != nil {
		t.Fatalf("unrelated tracker setting rejected: %v", err)
	}
	// The loader used by both restart and cleanup retains the upload account.
	retained, err := configstore.LoadLiveTestConfig(t.Context(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if retained.ImageHosting.LostimgAPI != cfg.ImageHosting.LostimgAPI {
		t.Fatal("cleanup lost its original image credential")
	}
	restarted := &Backend{
		repo:     repo,
		liveTest: policy,
		cfg:      *retained,
	}
	restartedActivator, err := restarted.runtimeActivator()
	if err != nil {
		t.Fatal(err)
	}
	retained.ImageHosting.LostimgAPI = "synthetic-different-image-key"
	assertActivationStage(t, restartedActivator.Activate(t.Context(), *retained), ActivationStageValidateStored)
}

func TestLiveTestRuntimeActivatorRejectsUnpublishedProfile(t *testing.T) {
	t.Parallel()
	repo := openRuntimeActivationTestRepo(t)
	policy, err := api.NewLiveTestPolicy("unpublished-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	backend := &Backend{repo: repo, liveTest: policy}
	if _, err := backend.runtimeActivator(); err == nil || backend.activator != nil {
		t.Fatalf("unpublished profile activated: %v", err)
	}
}
