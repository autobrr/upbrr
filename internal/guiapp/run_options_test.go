// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type closeCounter struct {
	count atomic.Int32
}

func (c *closeCounter) Close() error {
	c.count.Add(1)
	return nil
}

type closeCounterCore struct {
	closeCounter
	exportedMeta api.PreparedMetadata
	exportFound  bool
	exportErr    error
	importedMeta api.PreparedMetadata
	importedReq  api.Request
}

func (c *closeCounterCore) RunUpload(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *closeCounterCore) RunUploadPrepared(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *closeCounterCore) FetchMetadataPreview(context.Context, api.Request) (api.MetadataPreview, error) {
	return api.MetadataPreview{}, nil
}

func (c *closeCounterCore) FetchDescriptionBuilderPreview(context.Context, api.Request) (api.DescriptionBuilderPreview, error) {
	return api.DescriptionBuilderPreview{}, nil
}

func (c *closeCounterCore) FetchPreparationPreview(context.Context, api.Request) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (c *closeCounterCore) FetchTrackerDryRunPreview(context.Context, api.Request) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (c *closeCounterCore) CheckDupes(context.Context, api.Request) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, nil
}

func (c *closeCounterCore) BuildUploadReview(context.Context, api.Request) (api.UploadReview, error) {
	return api.UploadReview{}, nil
}

func (c *closeCounterCore) FetchScreenshotPlan(context.Context, api.Request) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{}, nil
}

func (c *closeCounterCore) GenerateScreenshots(context.Context, api.Request, []api.ScreenshotSelection, api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	return api.ScreenshotResult{}, nil
}

func (c *closeCounterCore) PreviewScreenshotFrame(context.Context, api.Request, float64) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, nil
}

func (c *closeCounterCore) DeleteScreenshot(context.Context, api.Request, string) error {
	return nil
}

func (c *closeCounterCore) DeleteTrackerImageURL(context.Context, api.Request, string) error {
	return nil
}

func (c *closeCounterCore) SaveFinalScreenshotSelections(context.Context, api.Request, []api.ScreenshotImage) error {
	return nil
}

func (c *closeCounterCore) ListUploadCandidates(context.Context, api.Request) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (c *closeCounterCore) ListUploadedImages(context.Context, api.Request) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (c *closeCounterCore) UploadImages(_ context.Context, _ api.Request, host string, screenshots []api.ScreenshotImage) (api.UploadImagesResult, error) {
	return api.UploadImagesResult{}, nil
}

func (c *closeCounterCore) DeleteUploadedImage(context.Context, api.Request, string, string) error {
	return nil
}

func (c *closeCounterCore) DiscoverPlaylists(context.Context, string) ([]api.PlaylistInfo, error) {
	return nil, nil
}

func (c *closeCounterCore) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return nil
}

func (c *closeCounterCore) LoadPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, nil
}

func (c *closeCounterCore) ListHistory(context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (c *closeCounterCore) GetHistoryOverview(context.Context, string) (api.HistoryOverview, error) {
	return api.HistoryOverview{}, nil
}

func (c *closeCounterCore) DeleteHistoryRelease(context.Context, string) error {
	return nil
}

func (c *closeCounterCore) DeleteAllHistoryReleases(context.Context) (int, error) {
	return 0, nil
}

func (c *closeCounterCore) RenderDescription(context.Context, string) (string, error) {
	return "", nil
}

func (c *closeCounterCore) FetchDescriptionBuilderGroupPreview(context.Context, api.Request) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *closeCounterCore) SaveDescriptionOverride(context.Context, api.Request, string) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *closeCounterCore) ExportGUICachedPreparedMeta(context.Context, api.Request) (api.PreparedMetadata, bool, error) {
	return c.exportedMeta, c.exportFound, c.exportErr
}

func (c *closeCounterCore) ImportPreparedMetadataForGUI(_ context.Context, req api.Request, meta api.PreparedMetadata) error {
	c.importedReq = req
	c.importedMeta = meta
	return nil
}

func TestBuildRunOptionsDefaults(t *testing.T) {
	t.Parallel()

	app := &App{cfg: config.Config{Logging: config.LoggingConfig{Level: "info"}}}
	opts, err := app.buildRunOptions(true, "")
	if err != nil {
		t.Fatalf("build run options: %v", err)
	}
	if !opts.Debug {
		t.Fatalf("expected debug enabled, got %#v", opts)
	}
	if opts.RunLogLevel != "" {
		t.Fatalf("expected empty explicit run log level, got %q", opts.RunLogLevel)
	}
}

func TestBuildRunOptionsEmptyLogLevelSkipsNormalization(t *testing.T) {
	t.Parallel()

	app := &App{}
	opts, err := app.buildRunOptions(false, "   ")
	if err != nil {
		t.Fatalf("build run options: %v", err)
	}
	if opts.RunLogLevel != "" {
		t.Fatalf("expected empty run log level for blank input, got %q", opts.RunLogLevel)
	}
}

func TestBuildRunOptionsRejectsInvalidLogLevel(t *testing.T) {
	t.Parallel()

	app := &App{}
	if _, err := app.buildRunOptions(false, "verbose"); err == nil {
		t.Fatal("expected invalid log level to fail")
	}
}

func TestCoreCloseDoesNotCloseInjectedRepository(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "guiapp.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer func() {
		_ = repo.Close()
	}()
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repo: %v", err)
	}

	coreSvc, err := core.New(api.CoreDependencies{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: repoPath},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		},
		Logger: api.NopLogger{},
		Services: api.ServiceSet{
			Filesystem: filesystem.NewValidator(),
		},
		Repository: repo,
	})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	if err := coreSvc.Close(); err != nil {
		t.Fatalf("close core: %v", err)
	}

	if err := repo.Save(context.Background(), db.FileMetadata{
		Path: "C:\\test\\movie.mkv",
	}); err != nil {
		t.Fatalf("expected repo to remain usable after per-run core close: %v", err)
	}
}

func TestTrackerUploadJobCloseResourcesIsIdempotent(t *testing.T) {
	t.Parallel()

	coreCloser := &closeCounterCore{}
	loggerCloser := &closeCounter{}
	job := &trackerUploadJob{
		core:   coreCloser,
		logger: loggerCloser,
	}

	job.closeResources()
	job.closeResources()

	if got := coreCloser.count.Load(); got != 1 {
		t.Fatalf("expected core close once, got %d", got)
	}
	if got := loggerCloser.count.Load(); got != 1 {
		t.Fatalf("expected logger close once, got %d", got)
	}
}

func TestSeedRunCorePreparedMetaCopiesPreparedMetadata(t *testing.T) {
	t.Parallel()

	source := &closeCounterCore{
		exportFound:  true,
		exportedMeta: api.PreparedMetadata{SourcePath: "C:\\releases\\movie.mkv"},
	}
	target := &closeCounterCore{}
	req := api.Request{
		Paths: []string{"C:\\releases\\movie.mkv"},
		Mode:  api.ModeGUI,
	}

	if err := guishared.SeedRunCorePreparedMeta(context.Background(), source, target, req); err != nil {
		t.Fatalf("seed run core prepared meta: %v", err)
	}
	if target.importedMeta.SourcePath != "C:\\releases\\movie.mkv" {
		t.Fatalf("expected imported source path, got %q", target.importedMeta.SourcePath)
	}
	if len(target.importedReq.Paths) != 1 || target.importedReq.Paths[0] != "C:\\releases\\movie.mkv" {
		t.Fatalf("expected import request paths to be preserved, got %#v", target.importedReq.Paths)
	}
}

func TestSeedRunCorePreparedMetaSkipsWhenNoCacheFound(t *testing.T) {
	t.Parallel()

	source := &closeCounterCore{}
	target := &closeCounterCore{}

	if err := guishared.SeedRunCorePreparedMeta(context.Background(), source, target, api.Request{
		Paths: []string{"C:\\releases\\movie.mkv"},
		Mode:  api.ModeGUI,
	}); err != nil {
		t.Fatalf("seed run core prepared meta: %v", err)
	}
	if target.importedMeta.SourcePath != "" {
		t.Fatalf("expected no metadata import when cache missing, got %#v", target.importedMeta)
	}
}
