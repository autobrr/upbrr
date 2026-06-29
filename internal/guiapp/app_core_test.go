// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
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

func TestFetchMetadataPropagatesSkipAutoTorrentSetting(t *testing.T) {
	t.Parallel()

	coreSvc := &closeCounterCore{}
	app := &App{
		cfg: config.Config{
			Metadata:           config.MetadataConfig{SkipAutoTorrent: true},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 3},
		},
		core: coreSvc,
	}

	_, err := app.FetchMetadata("C:\\releases\\Example.mkv", "", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil)
	if err != nil {
		t.Fatalf("fetch metadata: %v", err)
	}
	if !coreSvc.fetchReq.Options.SkipAutoTorrent {
		t.Fatalf("expected skip_auto_torrent request option, got %#v", coreSvc.fetchReq.Options)
	}
}

type guiSnapshotGuardCore struct {
	closeCounterCore
	wantScreens int
	errs        chan<- error
}

func (c *guiSnapshotGuardCore) check(req api.Request, method string) {
	if req.Options.Screens != c.wantScreens {
		select {
		case c.errs <- errors.New(method + " used mismatched runtime options"):
		default:
		}
	}
}

func (c *guiSnapshotGuardCore) FetchScreenshotPlan(_ context.Context, req api.Request) (api.ScreenshotPlan, error) {
	c.check(req, "FetchScreenshotPlan")
	return api.ScreenshotPlan{}, nil
}

func (c *guiSnapshotGuardCore) GenerateScreenshots(_ context.Context, req api.Request, _ []api.ScreenshotSelection, _ api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	c.check(req, "GenerateScreenshots")
	return api.ScreenshotResult{}, nil
}

func (c *guiSnapshotGuardCore) ListUploadCandidates(_ context.Context, req api.Request) ([]api.ScreenshotImage, error) {
	c.check(req, "ListUploadCandidates")
	return nil, nil
}

func (c *guiSnapshotGuardCore) ListUploadedImages(_ context.Context, req api.Request) ([]api.UploadedImageLink, error) {
	c.check(req, "ListUploadedImages")
	return nil, nil
}

func (c *guiSnapshotGuardCore) UploadImages(_ context.Context, req api.Request, _ string, _ []api.ScreenshotImage) (api.UploadImagesResult, error) {
	c.check(req, "UploadImages")
	return api.UploadImagesResult{}, nil
}

func (c *guiSnapshotGuardCore) RunUploadPrepared(_ context.Context, req api.Request) (api.Result, error) {
	c.check(req, "RunUploadPrepared")
	return api.Result{}, nil
}

func (c *guiSnapshotGuardCore) ExportGUICachedPreparedMeta(_ context.Context, req api.Request) (api.PreparedMetadata, bool, error) {
	c.check(req, "ExportGUICachedPreparedMeta")
	return api.PreparedMetadata{}, false, nil
}

func TestGUIRequestsUseSingleRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	errs := make(chan error, 1)
	repo, repoPath := openGUIConfigTestRepo(t, "gui-runtime-snapshot.db")
	cfgA := guiConfigTestConfig(repoPath)
	cfgA.ScreenshotHandling.Screens = 1
	cfgB := cfgA
	cfgB.ScreenshotHandling.Screens = 2
	app := &App{
		cfg:  cfgA,
		core: &guiSnapshotGuardCore{wantScreens: 1, errs: errs},
		repo: repo,
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				app.replaceRuntime(cfgB, &guiSnapshotGuardCore{wantScreens: 2, errs: errs}, nil)
			} else {
				app.replaceRuntime(cfgA, &guiSnapshotGuardCore{wantScreens: 1, errs: errs}, nil)
			}
		}
	})
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})

	for range 200 {
		if _, err := app.FetchScreenshotPlan("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}); err != nil {
			t.Fatalf("fetch screenshot plan: %v", err)
		}
		if _, err := app.GenerateScreenshots("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, api.ScreenshotPurposeFinal); err != nil {
			t.Fatalf("generate screenshots: %v", err)
		}
		if _, err := app.ListUploadCandidates("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}); err != nil {
			t.Fatalf("list upload candidates: %v", err)
		}
		if _, err := app.ListUploadedImages("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}); err != nil {
			t.Fatalf("list uploaded images: %v", err)
		}
		if _, err := app.UploadImages("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, "host", []api.ScreenshotImage{{Path: "image.jpg"}}); err != nil {
			t.Fatalf("upload images: %v", err)
		}
		_, _ = app.FetchTrackerDryRun("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, nil, nil, nil, false, false, "")
		select {
		case err := <-errs:
			t.Fatal(err)
		default:
		}
	}
}

func TestRunSingleTrackerUploadUsesJobUploadOptionsSnapshot(t *testing.T) {
	t.Parallel()

	errs := make(chan error, 1)
	repoPath := filepath.Join(t.TempDir(), "gui-upload-job.db")
	jobCfg := guiConfigTestConfig(repoPath)
	jobCfg.ScreenshotHandling.Screens = 1
	currentCfg := jobCfg
	currentCfg.ScreenshotHandling.Screens = 2
	app := &App{cfg: currentCfg}
	job := &trackerUploadJob{
		sourcePath:           "C:\\releases\\Example.mkv",
		uploadOptions:        buildRunUploadOptions(jobCfg, runOptions{}),
		runOptions:           runOptions{},
		core:                 &guiSnapshotGuardCore{wantScreens: 1, errs: errs},
		descriptionGroups:    nil,
		trackers:             []string{"BTN"},
		questionnaireAnswers: map[string]map[string]string{},
	}

	if _, err := app.runSingleTrackerUpload(context.Background(), job, "BTN"); err != nil {
		t.Fatalf("run single tracker upload: %v", err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func TestSaveConfigAcceptsSameDatabasePathAlias(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-dbpath-alias.db")
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}
	t.Cleanup(func() {
		if coreSvc := app.currentCore(); coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger := app.currentLogger(); logger != nil {
			_ = logger.Close()
		}
	})

	updated := initial
	updated.MainSettings.DBPath = filepath.Dir(repoPath) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(repoPath)
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	if err := app.SaveConfig(payload); err != nil {
		t.Fatalf("save config with DBPath alias: %v", err)
	}
	if got := app.currentConfig().MainSettings.DBPath; got != repoPath {
		t.Fatalf("runtime DBPath: got %q want %q", got, repoPath)
	}
}

func TestSaveConfigRejectsDifferentDatabasePath(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-dbpath-change.db")
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.MainSettings.DBPath = filepath.Join(t.TempDir(), "different.db")
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = app.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected DBPath change rejection")
	}
	if !strings.Contains(err.Error(), "requires restart") {
		t.Fatalf("expected restart error, got %v", err)
	}
}

func TestSaveConfigAppliesRuntimeConfigImmediately(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "save-config.db")
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
	initial := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	app := &App{
		cfg:  initial,
		repo: repo,
	}
	t.Cleanup(func() {
		if coreSvc := app.currentCore(); coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger := app.currentLogger(); logger != nil {
			_ = logger.Close()
		}
	})

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	updated.Metadata.OnlyID = true
	updated.ScreenshotHandling.Screens = 4
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	if err := app.SaveConfig(payload); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runtimeCfg := app.currentConfig()
	if !runtimeCfg.Metadata.SkipAutoTorrent || !runtimeCfg.Metadata.OnlyID {
		t.Fatalf("expected metadata settings applied, got %#v", runtimeCfg.Metadata)
	}
	if runtimeCfg.ScreenshotHandling.Screens != 4 {
		t.Fatalf("expected screenshots=4, got %d", runtimeCfg.ScreenshotHandling.Screens)
	}
	if app.currentCore() == nil {
		t.Fatal("expected runtime core to be rebuilt")
	}
	options := buildRunUploadOptions(runtimeCfg, runOptions{})
	if !options.SkipAutoTorrent || !options.OnlyID || options.Screens != 4 {
		t.Fatalf("expected upload options from saved config, got %#v", options)
	}
}

func TestSaveConfigRejectsInvalidEnvRuntimeConfig(t *testing.T) {
	t.Setenv("UA_DEFAULT_SCREENS", "0")

	repoPath := filepath.Join(t.TempDir(), "save-config-env.db")
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

	initial := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.ScreenshotHandling.Screens = 4
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = app.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected env-derived runtime validation error")
	}
	if !strings.Contains(err.Error(), "screenshot_handling.screens") {
		t.Fatalf("expected screens validation error, got %v", err)
	}
	if got := app.currentConfig().ScreenshotHandling.Screens; got != 1 {
		t.Fatalf("expected runtime config to remain unchanged, got screens=%d", got)
	}
	if app.currentCore() != nil {
		t.Fatal("expected runtime core not to be rebuilt")
	}
	if _, loadErr := config.LoadFromDatabase(context.Background(), repo); loadErr == nil {
		t.Fatal("expected invalid runtime config to be rejected before database save")
	}
}

func TestSaveConfigBuildRuntimeFailureLeavesDatabaseAndRuntimeUnchanged(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-runtime-build-failure.db")
	initial := guiConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	previousCore := &closeCounterCore{}
	app := &App{
		cfg:  initial,
		core: previousCore,
		repo: repo,
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	updated.Logging.Level = "invalid-level"
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = app.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected runtime build failure")
	}

	assertGUIStoredAndRuntimeConfigUnchanged(t, app, repo, initial, previousCore)
}

func TestGetConfigFallsBackToRuntimeConfigWhenDatabaseConfigMissing(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-get-missing-config.db")
	cfg := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  cfg,
		repo: repo,
	}

	payload, err := app.GetConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	exported, err := config.ImportFromJSONEncrypted(payload)
	if err != nil {
		t.Fatalf("parse exported config: %v", err)
	}
	if exported.MainSettings.DBPath != repoPath {
		t.Fatalf("DBPath: got %q want %q", exported.MainSettings.DBPath, repoPath)
	}
	if _, loadErr := config.LoadFromDatabase(context.Background(), repo); !errors.Is(loadErr, internalerrors.ErrNotFound) {
		t.Fatalf("fallback should not persist config rows, load err=%v", loadErr)
	}
}

func TestGetConfigFallbackUsesSingleRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoPath := filepath.Join(tempDir, "gui-empty-snapshot.db")
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

	pathA := filepath.Join(tempDir, "runtime-a.db")
	pathB := filepath.Join(tempDir, "runtime-b.db")
	cfgA := config.Config{
		MainSettings:       config.MainSettingsConfig{DBPath: pathA},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
	}
	cfgB := config.Config{
		MainSettings:       config.MainSettingsConfig{DBPath: pathB},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 2},
	}
	app := &App{
		cfg:  cfgA,
		repo: repo,
	}

	assertExport := func(want config.Config) {
		t.Helper()

		app.replaceRuntime(want, nil, nil)
		payload, err := app.GetConfig()
		if err != nil {
			t.Fatalf("get config: %v", err)
		}
		exported, err := config.ImportFromJSONEncrypted(payload)
		if err != nil {
			t.Fatalf("parse exported config: %v", err)
		}
		switch exported.MainSettings.DBPath {
		case pathA:
			if exported.ScreenshotHandling.Screens != cfgA.ScreenshotHandling.Screens {
				t.Fatalf("mixed fallback snapshot for %q: screens=%d", pathA, exported.ScreenshotHandling.Screens)
			}
		case pathB:
			if exported.ScreenshotHandling.Screens != cfgB.ScreenshotHandling.Screens {
				t.Fatalf("mixed fallback snapshot for %q: screens=%d", pathB, exported.ScreenshotHandling.Screens)
			}
		default:
			t.Fatalf("unexpected fallback DBPath %q", exported.MainSettings.DBPath)
		}
	}
	assertExport(cfgA)
	assertExport(cfgB)

	if _, loadErr := config.LoadFromDatabase(context.Background(), repo); !errors.Is(loadErr, internalerrors.ErrNotFound) {
		t.Fatalf("fallback should not persist config rows, load err=%v", loadErr)
	}
}

func TestExportConfigToPathFallsBackToRuntimeConfigWhenDatabaseConfigMissing(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-export-missing-config.db")
	cfg := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  cfg,
		repo: repo,
	}

	outputBase := filepath.Join(t.TempDir(), "config-export")
	outputPath, err := app.exportConfigToPath(context.Background(), outputBase)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	wantPath := outputBase + ".yaml"
	if outputPath != wantPath {
		t.Fatalf("output path: got %q want %q", outputPath, wantPath)
	}
	exported, err := config.ImportFromYAML(outputPath)
	if err != nil {
		t.Fatalf("parse exported config: %v", err)
	}
	if exported.MainSettings.DBPath != repoPath {
		t.Fatalf("DBPath: got %q want %q", exported.MainSettings.DBPath, repoPath)
	}
	if exported.Trackers.Trackers == nil {
		t.Fatal("expected fallback export to include nonnil tracker map")
	}
	if _, loadErr := config.LoadFromDatabase(context.Background(), repo); !errors.Is(loadErr, internalerrors.ErrNotFound) {
		t.Fatalf("fallback should not persist config rows, load err=%v", loadErr)
	}
}

func TestExportConfigToPathUsesDatabaseConfigWhenPresent(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-export-stored-config.db")
	runtimeCfg := guiConfigTestConfig(repoPath)
	runtimeCfg.ScreenshotHandling.Screens = 1
	storedCfg := guiConfigTestConfig(repoPath)
	storedCfg.ScreenshotHandling.Screens = 7
	if err := config.SaveToDatabase(context.Background(), &storedCfg, repo); err != nil {
		t.Fatalf("save stored config: %v", err)
	}
	app := &App{
		cfg:  runtimeCfg,
		repo: repo,
	}

	outputPath, err := app.exportConfigToPath(context.Background(), filepath.Join(t.TempDir(), "stored.yaml"))
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	exported, err := config.ImportFromYAML(outputPath)
	if err != nil {
		t.Fatalf("parse exported config: %v", err)
	}
	if exported.ScreenshotHandling.Screens != storedCfg.ScreenshotHandling.Screens {
		t.Fatalf("screens: got %d want %d", exported.ScreenshotHandling.Screens, storedCfg.ScreenshotHandling.Screens)
	}
}

func TestExportConfigToPathAuthorizesAgainstExportSnapshotDBPath(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-export-snapshot-auth.db")
	tempDir := filepath.Dir(repoPath)
	runtimeDBPath := filepath.Join(tempDir, "runtime", "state.db")
	storedDBPath := filepath.Join(tempDir, "stored", "state.db")
	writeGUIAuthFile(t, runtimeDBPath, false)
	writeGUIAuthFile(t, storedDBPath, true)

	runtimeCfg := guiConfigTestConfig(runtimeDBPath)
	runtimeCfg.MainSettings.TMDBAPI = "runtime-secret"
	storedCfg := guiConfigTestConfig(storedDBPath)
	storedCfg.MainSettings.TMDBAPI = "stored-secret"
	if err := config.SaveToDatabase(context.Background(), &storedCfg, repo); err != nil {
		t.Fatalf("save stored config: %v", err)
	}
	app := &App{
		cfg:  runtimeCfg,
		repo: repo,
	}

	outputPath, err := app.exportConfigToPath(context.Background(), filepath.Join(t.TempDir(), "stored.yaml"))
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	payload, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read exported config: %v", err)
	}
	exported := string(payload)
	if !strings.Contains(exported, "stored-secret") {
		t.Fatalf("expected plaintext stored secret authorized by stored DB path, payload=%s", exported)
	}
	if strings.Contains(exported, "runtime-secret") {
		t.Fatalf("export used runtime secret, payload=%s", exported)
	}
}

func TestExportConfigToPathFallbackRespectsUnencryptedExportAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		allowUnencryptedExport bool
		expectPlaintext        bool
		expectEncryptedMarker  bool
	}{
		{
			name:                   "allow unencrypted export",
			allowUnencryptedExport: true,
			expectPlaintext:        true,
			expectEncryptedMarker:  false,
		},
		{
			name:                   "deny unencrypted export",
			allowUnencryptedExport: false,
			expectPlaintext:        false,
			expectEncryptedMarker:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo, repoPath := openGUIConfigTestRepo(t, "gui-export-auth.db")
			writeGUIAuthFile(t, repoPath, tc.allowUnencryptedExport)
			cfg := guiConfigTestConfig(repoPath)
			cfg.MainSettings.TMDBAPI = "plain-secret"
			app := &App{
				cfg:  cfg,
				repo: repo,
			}

			outputPath, err := app.exportConfigToPath(context.Background(), filepath.Join(t.TempDir(), "auth.yaml"))
			if err != nil {
				t.Fatalf("export config: %v", err)
			}
			payload, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read exported config: %v", err)
			}
			exported := string(payload)
			if got := strings.Contains(exported, "plain-secret"); got != tc.expectPlaintext {
				t.Fatalf("plaintext presence = %t, want %t; payload=%s", got, tc.expectPlaintext, exported)
			}
			if got := strings.Contains(exported, "upbrr-enc:v1:"); got != tc.expectEncryptedMarker {
				t.Fatalf("encrypted marker presence = %t, want %t; payload=%s", got, tc.expectEncryptedMarker, exported)
			}
		})
	}
}

func TestExportConfigToPathBlankPathNoops(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-export-blank-path.db")
	app := &App{
		cfg:  guiConfigTestConfig(repoPath),
		repo: repo,
	}

	outputPath, err := app.exportConfigToPath(context.Background(), "   ")
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if outputPath != "" {
		t.Fatalf("output path: got %q want empty", outputPath)
	}
}

func TestSaveConfigMalformedWebAuthFailsBeforeConfigSave(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-malformed-auth.db")
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if err := os.WriteFile(authmaterial.AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed auth file: %v", err)
	}

	err = app.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected malformed web auth to fail before save")
	}

	assertGUIFailedConfigSaveRowsAbsent(t, repo)
	if got := app.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after failed auth sync")
	}
}

func TestImportConfigMalformedWebAuthFailsBeforeConfigSave(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-import-malformed-auth.db")
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	imported := initial
	imported.Metadata.SkipAutoTorrent = true
	path := exportGUIConfigYAML(t, &imported)
	if err := os.WriteFile(authmaterial.AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed auth file: %v", err)
	}

	_, err := app.importConfigFromPath(context.Background(), path)
	if err == nil {
		t.Fatal("expected malformed web auth to fail before imported config save")
	}

	assertGUIFailedConfigSaveRowsAbsent(t, repo)
	if got := app.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after failed auth sync")
	}
}

func TestSaveConfigHardCookieMigrationErrorDoesNotSaveOrInstallRuntime(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-cookie-migration-hard-error.db")
	initial := guiConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	app := &App{
		cfg:  initial,
		repo: repo,
		sharedCookieMigrator: func(context.Context, string, api.Logger) error {
			return errors.New("forced migration failure")
		},
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = app.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected hard cookie migration error")
	}
	if !strings.Contains(err.Error(), "forced migration failure") {
		t.Fatalf("expected forced migration failure, got %v", err)
	}
	assertGUIStoredAndRuntimeConfigUnchanged(t, app, repo, initial, nil)
}

func TestSaveConfigMigratesLegacyCookies(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-legacy-cookies.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	legacyPath := writeGUILegacyCookieFile(t, repoPath, "BLU", `{"session":"from-legacy"}`)
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if err := app.SaveConfig(payload); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy cookie file to be removed after migration, err=%v", err)
	}
	values, err := cookies.LoadTrackerCookieMap(context.Background(), repoPath, "BLU")
	if err != nil {
		t.Fatalf("LoadTrackerCookieMap: %v", err)
	}
	if got := values["session"]; got != "from-legacy" {
		t.Fatal("migrated session cookie mismatch")
	}
}

func TestImportConfigBuildRuntimeFailureLeavesDatabaseAndRuntimeUnchanged(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-import-runtime-build-failure.db")
	initial := guiConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	previousCore := &closeCounterCore{}
	app := &App{
		cfg:  initial,
		core: previousCore,
		repo: repo,
	}

	imported := initial
	imported.Metadata.SkipAutoTorrent = true
	imported.Logging.Level = "invalid-level"
	path := exportGUIConfigYAML(t, &imported)

	result, err := app.importConfigFromPath(context.Background(), path)
	if err == nil {
		t.Fatal("expected runtime build failure")
	}
	if result.Message != "" || len(result.Warnings) != 0 {
		t.Fatalf("expected empty import result on failed apply, got %#v", result)
	}

	assertGUIStoredAndRuntimeConfigUnchanged(t, app, repo, initial, previousCore)
}

func TestImportConfigHardCookieMigrationErrorDoesNotSaveOrInstallRuntime(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-import-cookie-migration-hard-error.db")
	initial := guiConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	previousCore := &closeCounterCore{}
	app := &App{
		cfg:  initial,
		core: previousCore,
		repo: repo,
		sharedCookieMigrator: func(context.Context, string, api.Logger) error {
			return errors.New("forced migration failure")
		},
	}

	imported := initial
	imported.Metadata.SkipAutoTorrent = true
	path := exportGUIConfigYAML(t, &imported)

	result, err := app.importConfigFromPath(context.Background(), path)
	if err == nil {
		t.Fatal("expected hard cookie migration error")
	}
	if !strings.Contains(err.Error(), "forced migration failure") {
		t.Fatalf("expected forced migration failure, got %v", err)
	}
	if result.Message != "" || len(result.Warnings) != 0 {
		t.Fatalf("expected empty import result on failed apply, got %#v", result)
	}
	assertGUIStoredAndRuntimeConfigUnchanged(t, app, repo, initial, previousCore)
}

func TestSaveConfigSyncsUsableWebAuthBeforeSave(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-usable-auth.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	if err := app.SaveConfig(payload); err != nil {
		t.Fatalf("save config: %v", err)
	}

	assertGUICookieAuthStatePresent(t, repo)
}

func TestSaveConfigLeavesConfigUnchangedWhenRepositorySaveFailsAfterCookieSync(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-cookie-rollback.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	installGUIFailMainSettingsTrigger(t, repo)
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = app.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected save config to fail")
	}

	assertGUIConfigRowsAbsent(t, repo)
	assertGUICookieAuthStatePresent(t, repo)
	if got := app.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after failed config save")
	}
}

func TestSaveConfigRepositoryRejectsAuthChangeAfterValidation(t *testing.T) {
	t.Parallel()

	repo, repoPath := openGUIConfigTestRepo(t, "gui-save-auth-drift.db")
	if err := authmaterial.BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := validateCookieAuthMaterial(repoPath); err != nil {
		t.Fatalf("prevalidate auth material: %v", err)
	}
	if err := os.WriteFile(authmaterial.AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("replace auth file: %v", err)
	}
	initial := guiConfigTestConfig(repoPath)
	app := &App{
		cfg:  initial,
		repo: repo,
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	err := app.saveConfigToRepository(context.Background(), &updated, repoPath)
	if err == nil {
		t.Fatal("expected auth drift to malformed helper to fail")
	}
	assertGUIFailedConfigSaveRowsAbsent(t, repo)
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

func TestGetLogExclusionsReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, app *App)
	}{
		{
			name: "missing",
		},
		{
			name: "stored empty",
			setup: func(t *testing.T, app *App) {
				t.Helper()

				if err := app.UpdateLogExclusions(nil); err != nil {
					t.Fatalf("update log exclusions: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := &App{repo: openGUIAppTestRepo(t)}
			if tt.setup != nil {
				tt.setup(t, app)
			}

			patterns, err := app.GetLogExclusions()
			if err != nil {
				t.Fatalf("get log exclusions: %v", err)
			}
			if patterns == nil {
				t.Fatal("expected non-nil empty exclusions")
			}
			if len(patterns) != 0 {
				t.Fatalf("expected no exclusions, got %#v", patterns)
			}
		})
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

func openGUIConfigTestRepo(t *testing.T, name string) (*db.SQLiteRepository, string) {
	t.Helper()

	repoPath := filepath.Join(t.TempDir(), name)
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
	return repo, repoPath
}

func guiConfigTestConfig(repoPath string) config.Config {
	return config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
}

func exportGUIConfigYAML(t *testing.T, cfg *config.Config) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.ExportToYAML(cfg, path); err != nil {
		t.Fatalf("export config yaml: %v", err)
	}
	return path
}

func writeGUILegacyCookieFile(t *testing.T, repoPath, trackerID, payload string) string {
	t.Helper()

	path, err := db.CookiePath(repoPath, trackerID+".json")
	if err != nil {
		t.Fatalf("resolve legacy cookie path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir legacy cookie dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write legacy cookie file: %v", err)
	}
	return path
}

func writeGUIAuthFile(t *testing.T, dbPath string, allowUnencryptedExport bool) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}
	authJSON := `{"username":"tester","password_hash":"hash","encryption_key_seed":"seed","allow_unencrypted_export":false}`
	if allowUnencryptedExport {
		authJSON = `{"username":"tester","password_hash":"hash","encryption_key_seed":"seed","allow_unencrypted_export":true}`
	}
	if err := os.WriteFile(authmaterial.AuthFilePath(dbPath), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
}

func assertGUIFailedConfigSaveRowsAbsent(t *testing.T, repo *db.SQLiteRepository) {
	t.Helper()

	for _, section := range []string{
		"MainSettings",
		"cookies_encryption_salt",
		"cookies_encryption_auth_state",
	} {
		var data string
		err := repo.RawDB().QueryRowContext(context.Background(),
			`SELECT data FROM config_settings WHERE section = ?`,
			section,
		).Scan(&data)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected %s to be absent after failed sync, got row=%q err=%v", section, data, err)
		}
	}
}

func assertGUIConfigRowsAbsent(t *testing.T, repo *db.SQLiteRepository) {
	t.Helper()

	var data string
	err := repo.RawDB().QueryRowContext(context.Background(),
		`SELECT data FROM config_settings WHERE section = ?`,
		"MainSettings",
	).Scan(&data)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected MainSettings to be absent after failed save, got row=%q err=%v", data, err)
	}
}

func assertGUICookieAuthStatePresent(t *testing.T, repo *db.SQLiteRepository) {
	t.Helper()

	var authState string
	if err := repo.RawDB().QueryRowContext(context.Background(),
		`SELECT data FROM config_settings WHERE section = ?`,
		"cookies_encryption_auth_state",
	).Scan(&authState); err != nil {
		t.Fatalf("query cookie auth state: %v", err)
	}
	if !strings.Contains(authState, "fingerprint") {
		t.Fatalf("expected synced cookie auth fingerprint, got %s", authState)
	}
}

func installGUIFailMainSettingsTrigger(t *testing.T, repo *db.SQLiteRepository) {
	t.Helper()

	if _, err := repo.RawDB().ExecContext(context.Background(), `
		CREATE TRIGGER fail_main_settings_save
		BEFORE INSERT ON config_settings
		WHEN NEW.section = 'MainSettings'
		BEGIN
			SELECT RAISE(FAIL, 'forced main settings failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
}

func assertGUIStoredAndRuntimeConfigUnchanged(t *testing.T, app *App, repo *db.SQLiteRepository, want config.Config, wantCore api.Core) {
	t.Helper()

	stored, err := config.LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("load stored config: %v", err)
	}
	if stored.Metadata.SkipAutoTorrent != want.Metadata.SkipAutoTorrent {
		t.Fatalf("stored skip_auto_torrent: got %v want %v", stored.Metadata.SkipAutoTorrent, want.Metadata.SkipAutoTorrent)
	}
	if stored.Logging.Level != want.Logging.Level {
		t.Fatalf("stored logging level: got %q want %q", stored.Logging.Level, want.Logging.Level)
	}

	runtimeCfg := app.currentConfig()
	if runtimeCfg.Metadata.SkipAutoTorrent != want.Metadata.SkipAutoTorrent {
		t.Fatalf("runtime skip_auto_torrent: got %v want %v", runtimeCfg.Metadata.SkipAutoTorrent, want.Metadata.SkipAutoTorrent)
	}
	if runtimeCfg.Logging.Level != want.Logging.Level {
		t.Fatalf("runtime logging level: got %q want %q", runtimeCfg.Logging.Level, want.Logging.Level)
	}
	if gotCore := app.currentCore(); gotCore != wantCore {
		t.Fatalf("runtime core changed: got %T want %T", gotCore, wantCore)
	}
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
		Logging:            config.LoggingConfig{Level: "error"},
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

	if err := app.applyConfig(context.Background(), cfg); err != nil {
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
		Logging:            config.LoggingConfig{Level: "error"},
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

	allow, err := app.allowUnencryptedExport(repoPath)
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
