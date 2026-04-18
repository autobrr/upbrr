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
