// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestFetchMetadataReportsCoreValidationFailure(t *testing.T) {
	t.Parallel()

	app := &App{coreInitErr: errors.New("invalid config")}

	_, err := app.FetchMetadata("/tmp/example.mkv", "", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "core unavailable") {
		t.Fatalf("expected core unavailable error, got %v", err)
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Fatalf("expected wrapped validation error, got %v", err)
	}
}

func TestListHistoryUsesRepositoryWhenCoreDisabled(t *testing.T) {
	t.Parallel()

	repo := openGUIAppTestRepo(t)
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "Example.mkv")
	updatedAt := time.Now().UTC().Add(-time.Hour)
	createdAt := time.Now().UTC()

	if err := repo.Save(ctx, db.FileMetadata{
		Path:       sourcePath,
		Title:      "Example",
		Source:     "BluRay",
		Resolution: "1080p",
		UpdatedAt:  updatedAt,
	}); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, db.UploadRecord{
		SourcePath: sourcePath,
		Tracker:    "HDB",
		Status:     "uploaded",
		CreatedAt:  createdAt,
	}); err != nil {
		t.Fatalf("create upload record: %v", err)
	}

	app := &App{
		repo:        repo,
		coreInitErr: errors.New("invalid config"),
	}

	entries, err := app.ListHistory()
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(entries))
	}
	if entries[0].SourcePath != sourcePath {
		t.Fatalf("unexpected source path: %q", entries[0].SourcePath)
	}
	if entries[0].LatestUploadStatus != "Uploaded" {
		t.Fatalf("expected normalized status, got %q", entries[0].LatestUploadStatus)
	}
}

func TestGetHistoryOverviewUsesRepositoryWhenCoreDisabled(t *testing.T) {
	t.Parallel()

	repo := openGUIAppTestRepo(t)
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "Example.mkv")
	updatedAt := time.Now().UTC().Add(-time.Hour)
	createdAt := time.Now().UTC()

	if err := repo.Save(ctx, db.FileMetadata{
		Path:       sourcePath,
		Title:      "Example",
		Source:     "WEB",
		Resolution: "2160p",
		UpdatedAt:  updatedAt,
	}); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, db.UploadRecord{
		SourcePath: sourcePath,
		Tracker:    "BHD",
		Status:     "failed",
		CreatedAt:  createdAt,
	}); err != nil {
		t.Fatalf("create upload record: %v", err)
	}
	if err := repo.SaveDescriptionOverride(ctx, db.DescriptionOverride{
		SourcePath:  sourcePath,
		GroupKey:    "unit3d",
		Description: "grouped override",
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save description override: %v", err)
	}

	app := &App{
		repo:        repo,
		coreInitErr: errors.New("invalid config"),
	}

	overview, err := app.GetHistoryOverview(sourcePath)
	if err != nil {
		t.Fatalf("get history overview: %v", err)
	}
	if overview.SourcePath != sourcePath {
		t.Fatalf("unexpected source path: %q", overview.SourcePath)
	}
	if overview.ReleaseTitle != "Example" {
		t.Fatalf("unexpected release title: %q", overview.ReleaseTitle)
	}
	if overview.StatusLabel != "Failed" {
		t.Fatalf("expected failed status label, got %q", overview.StatusLabel)
	}
	if len(overview.DescriptionOverrides) != 1 {
		t.Fatalf("expected 1 grouped description override, got %d", len(overview.DescriptionOverrides))
	}
	if overview.DescriptionOverrides[0].GroupKey != "unit3d" {
		t.Fatalf("expected grouped override key to be preserved, got %q", overview.DescriptionOverrides[0].GroupKey)
	}
	if overview.DescriptionOverride.GroupKey != "unit3d" {
		t.Fatalf("expected preferred description override key to be unit3d, got %q", overview.DescriptionOverride.GroupKey)
	}
}

func openGUIAppTestRepo(t *testing.T) *db.SQLiteRepository {
	t.Helper()

	repoPath := filepath.Join(t.TempDir(), "guiapp.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}
	return repo
}

func TestApplyConfigKeepsSharedRepositoryUsable(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "apply.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
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

	app := &App{
		cfg:  cfg,
		repo: repo,
	}
	t.Cleanup(func() {
		if app.core != nil {
			_ = app.core.Close()
		}
		if app.logger != nil {
			_ = app.logger.Close()
		}
	})

	if err := app.applyConfig(cfg); err != nil {
		t.Fatalf("apply config: %v", err)
	}
	if app.core == nil {
		t.Fatal("expected core to be initialized")
	}
	if err := app.core.Close(); err != nil {
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

func TestNewAppKeepsSharedRepositoryUsableAfterCoreClose(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "newapp.db")
	cfg := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	authPath := filepath.Join(filepath.Dir(repoPath), authmaterial.WebAuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"username":"tester","password_hash":"very-secret-password-hash","encryption_key_seed":"stable-seed-for-tests"}`), 0o600); err != nil {
		t.Fatalf("write web auth fixture: %v", err)
	}
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("export config: %v", err)
	}

	app, err := NewApp(configPath, true)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() {
		app.shutdown(context.Background())
	})

	if app.core == nil {
		t.Fatal("expected startup core to be initialized")
	}
	if err := app.core.Close(); err != nil {
		t.Fatalf("close core: %v", err)
	}
	if app.repo == nil {
		t.Fatal("expected shared repo to be initialized")
	}

	if err := app.repo.Save(context.Background(), db.FileMetadata{
		Path:      filepath.Join(t.TempDir(), "after-startup.mkv"),
		Title:     "After Startup",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("expected startup repo to remain usable after core close: %v", err)
	}
}

func TestNewAppClearsPersistedUIState(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "fresh-ui.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}
	if err := repo.SaveUIState(context.Background(), "state-a", "Upload", map[string]any{"activeTab": "upload"}); err != nil {
		t.Fatalf("save ui state: %v", err)
	}

	cfg := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "info"},
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	authPath := filepath.Join(filepath.Dir(repoPath), authmaterial.WebAuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"username":"tester","password_hash":"very-secret-password-hash","encryption_key_seed":"stable-seed-for-tests"}`), 0o600); err != nil {
		t.Fatalf("write web auth fixture: %v", err)
	}
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("export config: %v", err)
	}

	app, err := NewApp(configPath, true)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	t.Cleanup(func() {
		app.shutdown(context.Background())
	})

	stateList, err := app.ListUIStates()
	if err != nil {
		t.Fatalf("list ui states: %v", err)
	}
	if len(stateList.States) != 0 {
		t.Fatalf("expected startup to clear persisted UI state, got %#v", stateList.States)
	}
}

func TestAppAllowUnencryptedExportFromWebAuth(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "gui.db")
	authPath := filepath.Join(filepath.Dir(repoPath), authmaterial.WebAuthFileName)
	if err := os.WriteFile(authPath, []byte(`{"username":"tester","password_hash":"hash","allow_unencrypted_export":true}`), 0o600); err != nil {
		t.Fatalf("write web auth fixture: %v", err)
	}

	app := &App{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: repoPath},
		},
	}

	allow, err := app.allowUnencryptedExport()
	if err != nil {
		t.Fatalf("allowUnencryptedExport: %v", err)
	}
	if !allow {
		t.Fatal("expected allowUnencryptedExport to be true")
	}
}

func TestGetWebAuthStatusReportsMissingFile(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "gui.db")
	app := &App{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: repoPath},
		},
	}

	status, err := app.GetWebAuthStatus()
	if err != nil {
		t.Fatalf("GetWebAuthStatus: %v", err)
	}
	if status.Exists {
		t.Fatal("expected missing web auth file")
	}
	if !status.CanCreate {
		t.Fatal("expected status to allow creating web auth")
	}
	if status.Usable {
		t.Fatal("expected missing web auth to be unusable")
	}
}

func TestCreateWebAuthCreatesUsableAuthFile(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "gui.db")
	app := &App{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: repoPath},
		},
	}

	status, err := app.CreateWebAuth("tester", "very-secure-password")
	if err != nil {
		t.Fatalf("CreateWebAuth: %v", err)
	}
	if !status.Exists || !status.Usable {
		t.Fatalf("expected usable web auth after create, got %+v", status)
	}
	if status.Username != "tester" {
		t.Fatalf("expected username tester, got %q", status.Username)
	}
	if status.CanCreate {
		t.Fatal("expected create to be disabled after bootstrap")
	}

	authPath := authmaterial.AuthFilePath(repoPath)
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("expected auth file to exist: %v", err)
	}
}
