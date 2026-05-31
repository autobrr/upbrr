// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBackendApplyConfigKeepsSharedRepositoryUsable(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "backend.db")
	repo, err := db.OpenWithLogger(repoPath, nil)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	cfg := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}

	backend := &Backend{
		cfg:  cfg,
		repo: repo,
		hub:  newEventHub(),
	}
	t.Cleanup(func() {
		if backend.core != nil {
			_ = backend.core.Close()
		}
		if backend.logger != nil {
			_ = backend.logger.Close()
		}
	})

	if err := backend.applyConfig(cfg); err != nil {
		t.Fatalf("apply config: %v", err)
	}
	if backend.core == nil {
		t.Fatal("expected core to be initialized")
	}
	if err := backend.core.Close(); err != nil {
		t.Fatalf("close core: %v", err)
	}

	if err := repo.Save(context.Background(), db.FileMetadata{
		Path:      filepath.Join(t.TempDir(), "after-apply.mkv"),
		Title:     "After Apply",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("expected shared repo to remain usable after core close: %v", err)
	}
}

func TestNewBackendKeepsSharedRepositoryUsableAfterCoreClose(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "startup.db")
	cfg := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}

	backend, err := NewBackend(cfg, newEventHub())
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	t.Cleanup(func() {
		_ = backend.Close()
	})

	if backend.core == nil {
		t.Fatal("expected startup core to be initialized")
	}
	if err := backend.core.Close(); err != nil {
		t.Fatalf("close core: %v", err)
	}
	if backend.repo == nil {
		t.Fatal("expected startup repo to be initialized")
	}

	if err := backend.repo.Save(context.Background(), db.FileMetadata{
		Path:      filepath.Join(t.TempDir(), "after-startup.mkv"),
		Title:     "After Startup",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("expected startup repo to remain usable after core close: %v", err)
	}
}

func TestNewBackendClearsPersistedUIState(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "startup-state.db")
	repo, err := db.OpenWithLogger(repoPath, nil)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}
	if err := repo.SaveUIState(context.Background(), "state-a", "Stale", map[string]any{"path": "stale"}); err != nil {
		_ = repo.Close()
		t.Fatalf("save ui state: %v", err)
	}
	_ = repo.Close()

	cfg := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}
	backend, err := NewBackend(cfg, newEventHub())
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	t.Cleanup(func() {
		_ = backend.Close()
	})

	stateList, err := backend.ListUIStates()
	if err != nil {
		t.Fatalf("list ui states: %v", err)
	}
	if len(stateList.States) != 0 {
		t.Fatalf("expected startup to clear persisted UI state, got %#v", stateList.States)
	}
}

func TestBackendGetLogExclusionsReturnsEmptySliceWhenMissing(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "backend-log-exclusions.db")
	repo, err := db.OpenWithLogger(repoPath, nil)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	backend := &Backend{repo: repo}
	patterns, err := backend.GetLogExclusions()
	if err != nil {
		t.Fatalf("get log exclusions: %v", err)
	}
	if patterns == nil {
		t.Fatal("expected non-nil empty exclusions")
	}
	if len(patterns) != 0 {
		t.Fatalf("expected no exclusions, got %#v", patterns)
	}
}

func TestBackendFetchMetadataPropagatesSkipAutoTorrentSetting(t *testing.T) {
	t.Parallel()

	coreSvc := &preparedMetaTestCore{}
	backend := &Backend{
		cfg: config.Config{
			Metadata:           config.MetadataConfig{SkipAutoTorrent: true},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 3},
		},
		core: coreSvc,
		hub:  newEventHub(),
	}

	_, err := backend.FetchMetadata("session", "C:\\releases\\Example.mkv", "", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, false)
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}
	if !coreSvc.fetchReq.Options.SkipAutoTorrent {
		t.Fatalf("expected skip_auto_torrent request option, got %#v", coreSvc.fetchReq.Options)
	}
}

func TestBackendSaveConfigAppliesRuntimeConfigImmediately(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "backend-save-config.db")
	repo, err := db.OpenWithLogger(repoPath, nil)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	initial := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}
	t.Cleanup(func() {
		if coreSvc := backend.currentCore(); coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger := backend.currentLogger(); logger != nil {
			_ = logger.Close()
		}
	})

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	updated.Metadata.KeepImages = true
	updated.ScreenshotHandling.Screens = 5
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runtimeCfg := backend.currentConfig()
	if !runtimeCfg.Metadata.SkipAutoTorrent || !runtimeCfg.Metadata.KeepImages {
		t.Fatalf("expected metadata settings applied, got %#v", runtimeCfg.Metadata)
	}
	if runtimeCfg.ScreenshotHandling.Screens != 5 {
		t.Fatalf("expected screenshots=5, got %d", runtimeCfg.ScreenshotHandling.Screens)
	}
	if backend.currentCore() == nil {
		t.Fatal("expected runtime core to be rebuilt")
	}
	options := buildRunUploadOptions(runtimeCfg, runOptions{})
	if !options.SkipAutoTorrent || !options.KeepImages || options.Screens != 5 {
		t.Fatalf("expected upload options from saved config, got %#v", options)
	}
}

func TestBackendSaveConfigRejectsInvalidEnvRuntimeConfig(t *testing.T) {
	t.Setenv("UA_DEFAULT_SCREENS", "0")

	repoPath := filepath.Join(t.TempDir(), "backend-save-config-env.db")
	repo, err := db.OpenWithLogger(repoPath, nil)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	initial := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.ScreenshotHandling.Screens = 5
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = backend.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected env-derived runtime validation error")
	}
	if !strings.Contains(err.Error(), "screenshot_handling.screens") {
		t.Fatalf("expected screens validation error, got %v", err)
	}
	if got := backend.currentConfig().ScreenshotHandling.Screens; got != 1 {
		t.Fatalf("expected runtime config to remain unchanged, got screens=%d", got)
	}
	if backend.currentCore() != nil {
		t.Fatal("expected runtime core not to be rebuilt")
	}
	if _, loadErr := config.LoadFromDatabase(context.Background(), repo); loadErr == nil {
		t.Fatal("expected invalid runtime config to be rejected before database save")
	}
}

func TestBuildRunUploadOptionsPropagatesSkipAutoTorrent(t *testing.T) {
	t.Parallel()

	options := buildRunUploadOptions(config.Config{
		Metadata: config.MetadataConfig{SkipAutoTorrent: true},
	}, runOptions{})
	if !options.SkipAutoTorrent {
		t.Fatalf("expected skip_auto_torrent upload option, got %#v", options)
	}
}

func TestBackendExportConfigRespectsAllowUnencryptedExport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                          string
		allowUnencryptedExport        bool
		expectExportPlaintext         bool
		expectExportEncryptedEnvelope bool
		expectGetConfigEncrypted      bool
	}{
		{
			name:                          "allow unencrypted export",
			allowUnencryptedExport:        true,
			expectExportPlaintext:         true,
			expectExportEncryptedEnvelope: false,
			expectGetConfigEncrypted:      true,
		},
		{
			name:                          "deny unencrypted export",
			allowUnencryptedExport:        false,
			expectExportPlaintext:         false,
			expectExportEncryptedEnvelope: true,
			expectGetConfigEncrypted:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repoPath := filepath.Join(t.TempDir(), "export.db")
			cfg := config.Config{
				MainSettings:       config.MainSettingsConfig{TMDBAPI: "plain-secret", DBPath: repoPath},
				ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
				Logging:            config.LoggingConfig{Level: "info"},
			}

			backend, err := NewBackend(cfg, newEventHub())
			if err != nil {
				t.Fatalf("new backend: %v", err)
			}
			t.Cleanup(func() {
				_ = backend.Close()
			})

			authPath := filepath.Join(filepath.Dir(repoPath), AuthFileName)
			// Writing AuthFileName after NewBackend is intentional: ExportConfig reads auth lazily,
			// so this test ensures NewBackend does not cache allow_unencrypted_export state.
			authJSON := `{"username":"tester","password_hash":"hash","encryption_key_seed":"seed","allow_unencrypted_export":false}`
			if tc.allowUnencryptedExport {
				authJSON = `{"username":"tester","password_hash":"hash","encryption_key_seed":"seed","allow_unencrypted_export":true}`
			}
			if err := os.WriteFile(authPath, []byte(authJSON), 0o600); err != nil {
				t.Fatalf("write auth file: %v", err)
			}

			if err := config.SaveToDatabase(context.Background(), &cfg, backend.repo); err != nil {
				t.Fatalf("save config: %v", err)
			}

			exported, err := backend.ExportConfig()
			if err != nil {
				t.Fatalf("export config: %v", err)
			}
			if got := strings.Contains(exported, "plain-secret"); got != tc.expectExportPlaintext {
				t.Fatalf("ExportConfig plaintext presence = %t, want %t; payload=%s", got, tc.expectExportPlaintext, exported)
			}
			if got := strings.Contains(exported, "upbrr-enc:v1:"); got != tc.expectExportEncryptedEnvelope {
				t.Fatalf("ExportConfig encrypted marker presence = %t, want %t; payload=%s", got, tc.expectExportEncryptedEnvelope, exported)
			}

			editingPayload, err := backend.GetConfig()
			if err != nil {
				t.Fatalf("get config: %v", err)
			}
			if got := strings.Contains(editingPayload, "upbrr-enc:v1:"); got != tc.expectGetConfigEncrypted {
				t.Fatalf("GetConfig encrypted marker presence = %t, want %t; payload=%s", got, tc.expectGetConfigEncrypted, editingPayload)
			}
		})
	}
}
