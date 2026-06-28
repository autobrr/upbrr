// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/logging"
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
		Logging:            config.LoggingConfig{Level: "error"},
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
		Logging:            config.LoggingConfig{Level: "error"},
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

func TestBackendGetConfigFallsBackToRuntimeConfigWhenDatabaseConfigMissing(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "backend-empty-config.db")
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

	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded config: %v", err)
	}
	cfg.MainSettings.DBPath = repoPath
	cfg.Logging.Level = "error"
	backend := &Backend{
		cfg:  *cfg,
		repo: repo,
		hub:  newEventHub(),
	}

	payload, err := backend.GetConfig()
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
	if len(exported.Trackers.Trackers) == 0 {
		t.Fatal("expected runtime tracker defaults in fallback config")
	}
	if _, loadErr := config.LoadFromDatabase(context.Background(), repo); !errors.Is(loadErr, internalerrors.ErrNotFound) {
		t.Fatalf("fallback should not persist config rows, load err=%v", loadErr)
	}
}

func TestNormalizeExportableConfigDoesNotMutateSource(t *testing.T) {
	t.Parallel()

	source := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: " \t "},
		Trackers: config.TrackersConfig{
			DefaultTrackers: config.CSVList{"AITHER"},
			Trackers: map[string]config.TrackerConfig{
				"AITHER": {APIKey: "source-token"},
			},
		},
	}

	normalized, err := normalizeExportableConfig(&source, "runtime.db")
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	defaults, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatalf("load embedded defaults: %v", err)
	}
	for trackerName := range defaults.Trackers.Trackers {
		if _, ok := normalized.Trackers.Trackers[trackerName]; !ok {
			t.Fatalf("normalized config missing default tracker %q", trackerName)
		}
	}
	if normalized.MainSettings.DBPath != "runtime.db" {
		t.Fatalf("normalized DBPath: got %q want runtime.db", normalized.MainSettings.DBPath)
	}
	if source.MainSettings.DBPath != " \t " {
		t.Fatalf("source DBPath mutated: got %q", source.MainSettings.DBPath)
	}

	tracker := normalized.Trackers.Trackers["AITHER"]
	tracker.APIKey = "changed-token"
	normalized.Trackers.Trackers["AITHER"] = tracker
	normalized.Trackers.DefaultTrackers[0] = "BTN"

	if got := source.Trackers.Trackers["AITHER"].APIKey; got != "source-token" {
		t.Fatalf("source tracker map mutated: got %q", got)
	}
	if got := source.Trackers.DefaultTrackers[0]; got != "AITHER" {
		t.Fatalf("source default tracker slice mutated: got %q", got)
	}

	nilSource := config.Config{}
	nilNormalized, err := normalizeExportableConfig(&nilSource, "runtime.db")
	if err != nil {
		t.Fatalf("normalize nil trackers: %v", err)
	}
	if nilNormalized.Trackers.Trackers == nil {
		t.Fatal("expected normalized tracker map")
	}
	if nilNormalized.Trackers.DefaultTrackers == nil {
		t.Fatal("expected normalized default tracker list")
	}
	if nilSource.Trackers.Trackers != nil {
		t.Fatal("source tracker map mutated from nil")
	}
	if nilSource.Trackers.DefaultTrackers != nil {
		t.Fatal("source default tracker list mutated from nil")
	}
}

func TestBackendGetConfigFallbackUsesSingleRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoPath := filepath.Join(tempDir, "backend-empty-snapshot.db")
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
	backend := &Backend{
		cfg:  cfgA,
		repo: repo,
		hub:  newEventHub(),
	}

	assertExport := func(want config.Config) {
		t.Helper()

		backend.replaceRuntime(want, nil, nil)
		payload, err := backend.GetConfig()
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

func TestBackendGetConfigDatabaseConfigUsesSingleRuntimeSnapshotForDBPath(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	repoPath := filepath.Join(tempDir, "backend-db-snapshot.db")
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

	stored := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "stored"},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 9},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := config.SaveToDatabase(context.Background(), &stored, repo); err != nil {
		t.Fatalf("save stored config: %v", err)
	}

	pathA := filepath.Join(tempDir, "runtime-a.db")
	pathB := filepath.Join(tempDir, "runtime-b.db")
	cfgA := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "runtime-a", DBPath: pathA},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	cfgB := config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "runtime-b", DBPath: pathB},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 2},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	backend := &Backend{
		cfg:  cfgA,
		repo: repo,
		hub:  newEventHub(),
	}

	assertExport := func(wantDBPath string) {
		t.Helper()

		payload, err := backend.GetConfig()
		if err != nil {
			t.Fatalf("get config: %v", err)
		}
		exported, err := config.ImportFromJSONEncrypted(payload)
		if err != nil {
			t.Fatalf("parse exported config: %v", err)
		}
		if exported.ScreenshotHandling.Screens != stored.ScreenshotHandling.Screens {
			t.Fatalf("expected DB-loaded screens=%d, got %d", stored.ScreenshotHandling.Screens, exported.ScreenshotHandling.Screens)
		}
		if exported.MainSettings.TMDBAPI != stored.MainSettings.TMDBAPI {
			t.Fatalf("expected DB-loaded TMDB API, got %q", exported.MainSettings.TMDBAPI)
		}
		if exported.MainSettings.DBPath != wantDBPath {
			t.Fatalf("DBPath fallback: got %q want %q", exported.MainSettings.DBPath, wantDBPath)
		}
	}
	backend.replaceRuntime(cfgA, nil, nil)
	assertExport(pathA)
	backend.replaceRuntime(cfgB, nil, nil)
	assertExport(pathB)
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

type backendSnapshotGuardCore struct {
	preparedMetaTestCore
	wantScreens int
	errs        chan<- error
}

func (c *backendSnapshotGuardCore) check(req api.Request, method string) {
	if req.Options.Screens != c.wantScreens {
		select {
		case c.errs <- errors.New(method + " used mismatched runtime options"):
		default:
		}
	}
}

func (c *backendSnapshotGuardCore) FetchMetadataPreview(_ context.Context, req api.Request) (api.MetadataPreview, error) {
	c.check(req, "FetchMetadataPreview")
	return api.MetadataPreview{}, nil
}

func (c *backendSnapshotGuardCore) ListUploadCandidates(_ context.Context, req api.Request) ([]api.ScreenshotImage, error) {
	c.check(req, "ListUploadCandidates")
	return nil, nil
}

func (c *backendSnapshotGuardCore) ListUploadedImages(_ context.Context, req api.Request) ([]api.UploadedImageLink, error) {
	c.check(req, "ListUploadedImages")
	return nil, nil
}

func (c *backendSnapshotGuardCore) UploadImages(_ context.Context, req api.Request, _ string, _ []api.ScreenshotImage) (api.UploadImagesResult, error) {
	c.check(req, "UploadImages")
	return api.UploadImagesResult{}, nil
}

func (c *backendSnapshotGuardCore) RunUploadPrepared(_ context.Context, req api.Request) (api.Result, error) {
	c.check(req, "RunUploadPrepared")
	return api.Result{}, nil
}

func (c *backendSnapshotGuardCore) ExportGUICachedPreparedMeta(_ context.Context, req api.Request) (api.PreparedMetadata, bool, error) {
	c.check(req, "ExportGUICachedPreparedMeta")
	return api.PreparedMetadata{}, false, nil
}

func TestBackendRequestsUseSingleRuntimeSnapshot(t *testing.T) {
	t.Parallel()

	errs := make(chan error, 1)
	repo, repoPath := openBackendConfigTestRepo(t, "backend-runtime-snapshot.db")
	cfgA := backendConfigTestConfig(repoPath)
	cfgA.ScreenshotHandling.Screens = 1
	cfgB := cfgA
	cfgB.ScreenshotHandling.Screens = 2
	backend := &Backend{
		cfg:  cfgA,
		core: &backendSnapshotGuardCore{wantScreens: 1, errs: errs},
		repo: repo,
		hub:  newEventHub(),
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
				backend.replaceRuntime(cfgB, &backendSnapshotGuardCore{wantScreens: 2, errs: errs}, nil)
			} else {
				backend.replaceRuntime(cfgA, &backendSnapshotGuardCore{wantScreens: 1, errs: errs}, nil)
			}
		}
	})
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})

	for range 200 {
		if _, err := backend.FetchMetadata("session", "C:\\releases\\Example.mkv", "", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, false); err != nil {
			t.Fatalf("fetch metadata: %v", err)
		}
		if _, err := backend.UploadImages("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, "host", []api.ScreenshotImage{{Path: "image.jpg"}}); err != nil {
			t.Fatalf("upload images: %v", err)
		}
		if _, err := backend.ListUploadCandidates("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}); err != nil {
			t.Fatalf("list upload candidates: %v", err)
		}
		if _, err := backend.ListUploadedImages("C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}); err != nil {
			t.Fatalf("list uploaded images: %v", err)
		}
		_, _ = backend.FetchTrackerDryRun("session", "C:\\releases\\Example.mkv", api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, nil, nil, nil, nil, false, false, "")
		select {
		case err := <-errs:
			t.Fatal(err)
		default:
		}
	}
}

func TestBackendRunSingleTrackerUploadUsesJobUploadOptionsSnapshot(t *testing.T) {
	t.Parallel()

	errs := make(chan error, 1)
	repoPath := filepath.Join(t.TempDir(), "web-upload-job.db")
	jobCfg := backendConfigTestConfig(repoPath)
	jobCfg.ScreenshotHandling.Screens = 1
	currentCfg := jobCfg
	currentCfg.ScreenshotHandling.Screens = 2
	backend := &Backend{cfg: currentCfg}
	job := &trackerUploadJob{
		sourcePath:           "C:\\releases\\Example.mkv",
		uploadOptions:        buildRunUploadOptions(jobCfg, runOptions{}),
		runOptions:           runOptions{},
		core:                 &backendSnapshotGuardCore{wantScreens: 1, errs: errs},
		descriptionGroups:    nil,
		trackers:             []string{"BTN"},
		questionnaireAnswers: map[string]map[string]string{},
	}

	if _, err := backend.runSingleTrackerUpload(context.Background(), job, "BTN"); err != nil {
		t.Fatalf("run single tracker upload: %v", err)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func TestBackendSaveConfigAcceptsSameDatabasePathAlias(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-dbpath-alias.db")
	initial := backendConfigTestConfig(repoPath)
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
	updated.MainSettings.DBPath = filepath.Dir(repoPath) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(repoPath)
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("save config with DBPath alias: %v", err)
	}
	if got := backend.currentConfig().MainSettings.DBPath; got != repoPath {
		t.Fatalf("runtime DBPath: got %q want %q", got, repoPath)
	}
}

func TestBackendSaveConfigRejectsDifferentDatabasePath(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-dbpath-change.db")
	initial := backendConfigTestConfig(repoPath)
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.MainSettings.DBPath = filepath.Join(t.TempDir(), "different.db")
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = backend.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected DBPath change rejection")
	}
	if !strings.Contains(err.Error(), "requires restart") {
		t.Fatalf("expected restart error, got %v", err)
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
		Logging:            config.LoggingConfig{Level: "error"},
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

func TestBackendSaveConfigAfterInvalidStartupMigratesLegacyCookies(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "backend-save-invalid-startup-cookies.db")
	if err := BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth file: %v", err)
	}
	legacyPath := writeBackendLegacyCookieFile(t, repoPath, "BLU", `{"session":"from-legacy"}`)

	startupCfg := backendConfigTestConfig(repoPath)
	startupCfg.MainSettings.TMDBAPI = ""
	backend, err := NewBackend(startupCfg, newEventHub())
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	t.Cleanup(func() {
		_ = backend.Close()
	})
	if backend.currentCore() != nil {
		t.Fatal("expected invalid startup to leave core disabled")
	}

	repaired := backendConfigTestConfig(repoPath)
	payload, err := config.ExportToJSON(&repaired)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("save repaired config: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy cookie file to be removed after migration, err=%v", err)
	}
	values, err := cookies.LoadTrackerCookieMap(context.Background(), repoPath, "BLU")
	if err != nil {
		t.Fatalf("load migrated tracker cookies: %v", err)
	}
	if got := values["session"]; got != "from-legacy" {
		t.Fatalf("migrated session cookie: got %q want %q", got, "from-legacy")
	}
}

func TestBackendSaveConfigRetriesLegacyCookieMigrationAfterAuthAppears(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "backend-save-cookie-retry.db")
	legacyPath := writeBackendLegacyCookieFile(t, repoPath, "BLU", `{"session":"from-retry"}`)

	startupCfg := backendConfigTestConfig(repoPath)
	startupCfg.MainSettings.TMDBAPI = ""
	backend, err := NewBackend(startupCfg, newEventHub())
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	t.Cleanup(func() {
		_ = backend.Close()
	})

	repaired := backendConfigTestConfig(repoPath)
	payload, err := config.ExportToJSON(&repaired)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("save repaired config without auth: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("expected legacy cookie file to remain without auth helper: %v", err)
	}

	if err := BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth file: %v", err)
	}
	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("retry save repaired config: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy cookie file to be removed after retry, err=%v", err)
	}
	values, err := cookies.LoadTrackerCookieMap(context.Background(), repoPath, "BLU")
	if err != nil {
		t.Fatalf("load migrated tracker cookies: %v", err)
	}
	if got := values["session"]; got != "from-retry" {
		t.Fatalf("migrated session cookie after retry: got %q want %q", got, "from-retry")
	}
}

func TestBackendLogStreamContinuesAcrossSaveConfigRuntimeReplacement(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-log-stream.db")
	initial := backendConfigTestConfig(repoPath)
	initial.Logging.Level = "info"
	initialLogger, err := logging.New(initial.Logging, repoPath)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	initialLogger.SetConsoleOutput(io.Discard, io.Discard)
	backend := &Backend{
		cfg:     initial,
		logger:  initialLogger,
		repo:    repo,
		hub:     newEventHub(),
		streams: make(map[string]*backendLogStream),
	}
	t.Cleanup(func() {
		backend.stopAllLogStreams()
		if coreSvc := backend.currentCore(); coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger := backend.currentLogger(); logger != nil {
			_ = logger.Close()
		}
	})
	events, unsubscribe := backend.hub.Subscribe("session-save")
	t.Cleanup(unsubscribe)

	streamID, err := backend.StartLogStream("session-save")
	if err != nil {
		t.Fatalf("start log stream: %v", err)
	}
	initialLogger.Infof("before save")
	expectLogStreamMessage(t, events, streamID, "before save")

	updated := initial
	updated.ScreenshotHandling.Screens = 3
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if backend.currentLogger() == initialLogger {
		t.Fatal("expected save config to replace logger")
	}

	drainLogStreamEvents(events)
	initialLogger.Infof("stale after save")
	assertNoLogStreamEvent(t, events, "old logger should be unsubscribed after save rebind")
	backend.currentLogger().Infof("after save")
	expectLogStreamMessage(t, events, streamID, "after save")
}

func TestBackendStartLogStreamRejectsNilHubWithoutRegisteringStream(t *testing.T) {
	t.Parallel()

	logger, err := logging.New(config.LoggingConfig{Level: "error"}, filepath.Join(t.TempDir(), "nil-hub-log-stream.db"))
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() {
		_ = logger.Close()
	})
	backend := &Backend{
		logger:  logger,
		streams: make(map[string]*backendLogStream),
	}

	streamID, err := backend.StartLogStream("session-nil-hub")
	if err == nil {
		t.Fatal("expected nil hub error")
	}
	if !strings.Contains(err.Error(), "event hub not initialized") {
		t.Fatalf("expected event hub error, got %v", err)
	}
	if streamID != "" {
		t.Fatalf("expected empty stream ID, got %q", streamID)
	}
	backend.streamMu.Lock()
	streamCount := len(backend.streams)
	backend.streamMu.Unlock()
	if streamCount != 0 {
		t.Fatalf("expected no registered streams, got %d", streamCount)
	}
}

func TestBackendLogStreamContinuesAcrossImportConfigRuntimeReplacement(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-import-log-stream.db")
	initial := backendConfigTestConfig(repoPath)
	initial.Logging.Level = "info"
	initialLogger, err := logging.New(initial.Logging, repoPath)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	initialLogger.SetConsoleOutput(io.Discard, io.Discard)
	backend := &Backend{
		cfg:     initial,
		logger:  initialLogger,
		repo:    repo,
		hub:     newEventHub(),
		streams: make(map[string]*backendLogStream),
	}
	t.Cleanup(func() {
		backend.stopAllLogStreams()
		if coreSvc := backend.currentCore(); coreSvc != nil {
			_ = coreSvc.Close()
		}
		if logger := backend.currentLogger(); logger != nil {
			_ = logger.Close()
		}
	})
	events, unsubscribe := backend.hub.Subscribe("session-import")
	t.Cleanup(unsubscribe)

	streamID, err := backend.StartLogStream("session-import")
	if err != nil {
		t.Fatalf("start log stream: %v", err)
	}
	initialLogger.Infof("before import")
	expectLogStreamMessage(t, events, streamID, "before import")

	imported := initial
	imported.ScreenshotHandling.Screens = 4
	content := exportConfigYAMLString(t, &imported)
	if _, _, err := backend.ImportConfig("config.yaml", content); err != nil {
		t.Fatalf("import config: %v", err)
	}
	if backend.currentLogger() == initialLogger {
		t.Fatal("expected import config to replace logger")
	}

	drainLogStreamEvents(events)
	initialLogger.Infof("stale after import")
	assertNoLogStreamEvent(t, events, "old logger should be unsubscribed after import rebind")
	backend.currentLogger().Infof("after import")
	expectLogStreamMessage(t, events, streamID, "after import")
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
		Logging:            config.LoggingConfig{Level: "error"},
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

func TestBackendSaveConfigBuildRuntimeFailureDoesNotPersist(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-build-failure.db")
	initial := backendConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	updated.Logging.FileEnabled = true
	updated.Logging.MaxTotalSizeMB = 1
	updated.Logging.MaxFiles = 1
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	blockBackendLogDir(t, repoPath)

	err = backend.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected runtime build failure")
	}

	assertStoredSkipAutoTorrentUnset(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config to remain unchanged")
	}
	if backend.currentCore() != nil {
		t.Fatal("expected runtime core not to be rebuilt")
	}
}

func TestBackendImportConfigBuildRuntimeFailureDoesNotPersist(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-import-build-failure.db")
	initial := backendConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	imported := initial
	imported.Metadata.SkipAutoTorrent = true
	imported.Logging.FileEnabled = true
	imported.Logging.MaxTotalSizeMB = 1
	imported.Logging.MaxFiles = 1
	content := exportConfigYAMLString(t, &imported)
	blockBackendLogDir(t, repoPath)

	_, _, err := backend.ImportConfig("config.yaml", content)
	if err == nil {
		t.Fatal("expected runtime build failure")
	}

	assertStoredSkipAutoTorrentUnset(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config to remain unchanged")
	}
	if backend.currentCore() != nil {
		t.Fatal("expected runtime core not to be rebuilt")
	}
}

func TestBackendSaveConfigMalformedWebAuthFailsBeforeConfigSave(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-malformed-auth.db")
	initial := backendConfigTestConfig(repoPath)
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if err := os.WriteFile(AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed auth file: %v", err)
	}

	err = backend.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected malformed web auth to fail before save")
	}

	assertFailedConfigSaveRowsAbsent(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after failed auth sync")
	}
}

func TestBackendImportConfigMalformedWebAuthFailsBeforeConfigSave(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-import-malformed-auth.db")
	initial := backendConfigTestConfig(repoPath)
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	imported := initial
	imported.Metadata.SkipAutoTorrent = true
	content := exportConfigYAMLString(t, &imported)
	if err := os.WriteFile(AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed auth file: %v", err)
	}

	_, _, err := backend.ImportConfig("config.yaml", content)
	if err == nil {
		t.Fatal("expected malformed web auth to fail before imported config save")
	}

	assertFailedConfigSaveRowsAbsent(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after failed auth sync")
	}
}

func TestBackendSaveConfigSyncsUsableWebAuthBeforeSave(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-usable-auth.db")
	if err := BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	initial := backendConfigTestConfig(repoPath)
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	if err := backend.SaveConfig(payload); err != nil {
		t.Fatalf("save config: %v", err)
	}

	assertCookieAuthStatePresent(t, repo)
}

func TestBackendSaveConfigLeavesConfigUnchangedWhenRepositorySaveFailsAfterCookieSync(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-cookie-rollback.db")
	if err := BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	installBackendFailMainSettingsTrigger(t, repo)
	initial := backendConfigTestConfig(repoPath)
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	payload, err := config.ExportToJSON(&updated)
	if err != nil {
		t.Fatalf("export config: %v", err)
	}

	err = backend.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected save config to fail")
	}

	assertConfigRowsAbsent(t, repo)
	assertCookieAuthStatePresent(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after failed config save")
	}
}

func TestBackendSaveConfigHardCookieMigrationErrorDoesNotInstallRuntime(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-cookie-migration-hard-error.db")
	initial := backendConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
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

	err = backend.SaveConfig(payload)
	if err == nil {
		t.Fatal("expected hard cookie migration error")
	}
	if !strings.Contains(err.Error(), "forced migration failure") {
		t.Fatalf("expected forced migration failure, got %v", err)
	}
	assertStoredSkipAutoTorrentUnset(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after hard migration failure")
	}
	if backend.currentCore() != nil {
		t.Fatal("expected runtime core not to be installed after hard migration failure")
	}
}

func TestBackendImportConfigHardCookieMigrationErrorPropagates(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-import-cookie-migration-hard-error.db")
	initial := backendConfigTestConfig(repoPath)
	if err := config.SaveToDatabase(context.Background(), &initial, repo); err != nil {
		t.Fatalf("save initial config: %v", err)
	}
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
		sharedCookieMigrator: func(context.Context, string, api.Logger) error {
			return errors.New("forced migration failure")
		},
	}

	imported := initial
	imported.Metadata.SkipAutoTorrent = true
	content := exportConfigYAMLString(t, &imported)

	_, _, err := backend.ImportConfig("config.yaml", content)
	if err == nil {
		t.Fatal("expected hard cookie migration error")
	}
	if !strings.Contains(err.Error(), "forced migration failure") {
		t.Fatalf("expected forced migration failure, got %v", err)
	}
	assertStoredSkipAutoTorrentUnset(t, repo)
	if got := backend.currentConfig().Metadata.SkipAutoTorrent; got {
		t.Fatal("expected runtime config not to be applied after imported hard migration failure")
	}
	if backend.currentCore() != nil {
		t.Fatal("expected runtime core not to be installed after imported hard migration failure")
	}
}

func TestBackendSaveConfigRepositoryRejectsAuthChangeAfterValidation(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-save-auth-drift.db")
	if err := BootstrapAuthFile(repoPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	if err := validateCookieAuthMaterial(repoPath); err != nil {
		t.Fatalf("prevalidate auth material: %v", err)
	}
	if err := os.WriteFile(AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("replace auth file: %v", err)
	}
	initial := backendConfigTestConfig(repoPath)
	backend := &Backend{
		cfg:  initial,
		repo: repo,
		hub:  newEventHub(),
	}

	updated := initial
	updated.Metadata.SkipAutoTorrent = true
	err := backend.saveConfigToRepository(context.Background(), &updated, repoPath)
	if err == nil {
		t.Fatal("expected auth drift to malformed helper to fail")
	}
	assertFailedConfigSaveRowsAbsent(t, repo)
}

func openBackendConfigTestRepo(t *testing.T, name string) (*db.SQLiteRepository, string) {
	t.Helper()

	repoPath := filepath.Join(t.TempDir(), name)
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
	return repo, repoPath
}

func backendConfigTestConfig(repoPath string) config.Config {
	return config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
}

func writeBackendLegacyCookieFile(t *testing.T, repoPath, trackerID, payload string) string {
	t.Helper()

	legacyPath, err := db.CookiePath(repoPath, trackerID+".json")
	if err != nil {
		t.Fatalf("resolve legacy cookie path: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write legacy cookie file: %v", err)
	}
	return legacyPath
}

func exportConfigYAMLString(t *testing.T, cfg *config.Config) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.ExportToYAML(cfg, path); err != nil {
		t.Fatalf("export config yaml: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config yaml: %v", err)
	}
	return string(raw)
}

func assertFailedConfigSaveRowsAbsent(t *testing.T, repo *db.SQLiteRepository) {
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

func assertConfigRowsAbsent(t *testing.T, repo *db.SQLiteRepository) {
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

func expectLogStreamMessage(t *testing.T, events <-chan serverEvent, streamID string, want string) {
	t.Helper()

	select {
	case event := <-events:
		if event.Name != "log:stream:"+streamID {
			t.Fatalf("event name: got %q want %q", event.Name, "log:stream:"+streamID)
		}
		var entry logging.Entry
		if err := json.Unmarshal(event.Data, &entry); err != nil {
			t.Fatalf("decode log entry: %v", err)
		}
		if entry.Message != want {
			t.Fatalf("log message: got %q want %q", entry.Message, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for log stream message %q", want)
	}
}

func assertNoLogStreamEvent(t *testing.T, events <-chan serverEvent, reason string) {
	t.Helper()

	select {
	case event := <-events:
		t.Fatalf("%s: unexpected event %q", reason, event.Name)
	case <-time.After(100 * time.Millisecond):
	}
}

func drainLogStreamEvents(events <-chan serverEvent) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func assertStoredSkipAutoTorrentUnset(t *testing.T, repo *db.SQLiteRepository) {
	t.Helper()

	var rawMetadata string
	if err := repo.RawDB().QueryRowContext(context.Background(),
		`SELECT data FROM config_settings WHERE section = ?`,
		"Metadata",
	).Scan(&rawMetadata); err != nil {
		t.Fatalf("load raw metadata config section: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		t.Fatalf("decode raw metadata config section: %v", err)
	}
	rawValue, ok := metadata["SkipAutoTorrent"].(bool)
	if !ok {
		t.Fatalf("raw metadata config section missing SkipAutoTorrent: %s", rawMetadata)
	}
	if rawValue {
		t.Fatal("raw stored skip_auto_torrent changed unexpectedly")
	}

	stored, err := config.LoadFromDatabase(context.Background(), repo)
	if err != nil {
		t.Fatalf("load stored config: %v", err)
	}
	if stored.Metadata.SkipAutoTorrent {
		t.Fatal("stored skip_auto_torrent changed unexpectedly")
	}
}

func blockBackendLogDir(t *testing.T, dbPath string) {
	t.Helper()

	logDir := filepath.Join(filepath.Dir(dbPath), "logs")
	if err := os.WriteFile(logDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block log dir: %v", err)
	}
}

func assertCookieAuthStatePresent(t *testing.T, repo *db.SQLiteRepository) {
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

func installBackendFailMainSettingsTrigger(t *testing.T, repo *db.SQLiteRepository) {
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
				Logging:            config.LoggingConfig{Level: "error"},
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

func TestBackendExportConfigAuthorizesAgainstExportSnapshotDBPath(t *testing.T) {
	t.Parallel()

	repo, repoPath := openBackendConfigTestRepo(t, "backend-export-snapshot-auth.db")
	tempDir := filepath.Dir(repoPath)
	pathA := filepath.Join(tempDir, "runtime-a", "state.db")
	pathB := filepath.Join(tempDir, "runtime-b", "state.db")
	writeBackendAuthFile(t, pathA, false)
	writeBackendAuthFile(t, pathB, true)

	cfgA := backendConfigTestConfig(pathA)
	cfgA.MainSettings.TMDBAPI = "snapshot-secret-a"
	cfgB := backendConfigTestConfig(pathB)
	cfgB.MainSettings.TMDBAPI = "snapshot-secret-b"
	if err := config.SaveToDatabase(context.Background(), &cfgB, repo); err != nil {
		t.Fatalf("save stored config: %v", err)
	}
	backend := &Backend{
		cfg:  cfgA,
		repo: repo,
		hub:  newEventHub(),
	}

	snapshotCfg, authDBPath, err := backend.exportableConfig()
	if err != nil {
		t.Fatalf("exportable config: %v", err)
	}
	if snapshotCfg.MainSettings.DBPath != pathB {
		t.Fatalf("snapshot DBPath: got %q want %q", snapshotCfg.MainSettings.DBPath, pathB)
	}
	if authDBPath != pathB {
		t.Fatalf("auth DBPath: got %q want %q", authDBPath, pathB)
	}

	backend.replaceRuntime(cfgA, nil, nil)
	allowPlaintext, err := backend.allowUnencryptedExport(authDBPath)
	if err != nil {
		t.Fatalf("allow unencrypted export: %v", err)
	}
	if !allowPlaintext {
		t.Fatal("expected plaintext export to remain authorized by snapshot DBPath after runtime replacement")
	}

	payload, err := backend.ExportConfig()
	if err != nil {
		t.Fatalf("export config: %v", err)
	}
	if !strings.Contains(payload, "snapshot-secret-b") {
		t.Fatalf("expected plaintext stored snapshot secret in export, payload=%s", payload)
	}
	if strings.Contains(payload, "snapshot-secret-a") {
		t.Fatalf("export used runtime secret, payload=%s", payload)
	}

	editingPayload, err := backend.GetConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if !strings.Contains(editingPayload, "upbrr-enc:v1:") {
		t.Fatalf("GetConfig should remain encrypted, payload=%s", editingPayload)
	}
}

func TestBackendAllowUnencryptedExportMalformedAuthReturnsError(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "malformed-auth.db")
	if err := os.WriteFile(AuthFilePath(repoPath), []byte(`{`), 0o600); err != nil {
		t.Fatalf("write malformed auth file: %v", err)
	}

	allowPlaintext, err := (&Backend{}).allowUnencryptedExport(repoPath)
	if err == nil {
		t.Fatal("expected malformed web auth to return an error")
	}
	if allowPlaintext {
		t.Fatal("malformed web auth must not allow plaintext export")
	}
}

func writeBackendAuthFile(t *testing.T, dbPath string, allowUnencryptedExport bool) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}
	authJSON := `{"username":"tester","password_hash":"hash","encryption_key_seed":"seed","allow_unencrypted_export":false}`
	if allowUnencryptedExport {
		authJSON = `{"username":"tester","password_hash":"hash","encryption_key_seed":"seed","allow_unencrypted_export":true}`
	}
	if err := os.WriteFile(AuthFilePath(dbPath), []byte(authJSON), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
}
