// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type closeCounter struct {
	count      atomic.Int32
	closeErr   error
	panicValue any
}

func (c *closeCounter) Close() error {
	c.count.Add(1)
	if c.panicValue != nil {
		panic(c.panicValue)
	}
	return c.closeErr
}

type closeCounterCore struct {
	closeCounter
	exportedMeta      api.PreparedMetadata
	exportFound       bool
	exportErr         error
	expectedExportReq *api.Request
	importedMeta      api.PreparedMetadata
	importedReq       api.Request
	fetchReq          api.Request
	dupeSummary       api.DupeCheckSummary
	dupeCalls         atomic.Int32
	uploads           []uploadPreparedResponse
	uploadCalls       int
}

type uploadPreparedResponse struct {
	result       api.Result
	err          error
	beforeReturn func()
	panicValue   any
}

func (c *closeCounterCore) RunUpload(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *closeCounterCore) RunUploadPrepared(context.Context, api.Request) (api.Result, error) {
	if c.uploadCalls < len(c.uploads) {
		response := c.uploads[c.uploadCalls]
		c.uploadCalls++
		if response.beforeReturn != nil {
			response.beforeReturn()
		}
		if response.panicValue != nil {
			panic(response.panicValue)
		}
		return response.result, response.err
	}
	return api.Result{}, nil
}

func (c *closeCounterCore) FetchMetadataPreview(_ context.Context, req api.Request) (api.MetadataPreview, error) {
	c.fetchReq = req
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
	c.dupeCalls.Add(1)
	return c.dupeSummary, nil
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

func (c *closeCounterCore) ImportMenuImages(context.Context, api.Request, []string) error {
	return nil
}

func (c *closeCounterCore) ListUploadCandidates(context.Context, api.Request) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (c *closeCounterCore) ListUploadedImages(context.Context, api.Request) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (c *closeCounterCore) UploadImages(_ context.Context, _ api.Request, _ string, _ []api.ScreenshotImage) (api.UploadImagesResult, error) {
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

func (c *closeCounterCore) ExportGUICachedPreparedMeta(_ context.Context, req api.Request) (api.PreparedMetadata, bool, error) {
	if err := assertPreparedMetaExportRequest(req, c.expectedExportReq); err != nil {
		return api.PreparedMetadata{}, false, err
	}
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
	opts, err := app.buildRunOptions(true, false, "")
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
	opts, err := app.buildRunOptions(false, false, "   ")
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
	if _, err := app.buildRunOptions(false, false, "verbose"); err == nil {
		t.Fatal("expected invalid log level to fail")
	}
}

func TestBuildRunOptionsPropagatesNoSeed(t *testing.T) {
	t.Parallel()

	app := &App{}
	opts, err := app.buildRunOptions(false, true, "")
	if err != nil {
		t.Fatalf("build run options: %v", err)
	}
	if !opts.NoSeed {
		t.Fatalf("expected no-seed enabled, got %#v", opts)
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

	_ = job.closeResources()
	_ = job.closeResources()

	if got := coreCloser.count.Load(); got != 1 {
		t.Fatalf("expected core close once, got %d", got)
	}
	if got := loggerCloser.count.Load(); got != 1 {
		t.Fatalf("expected logger close once, got %d", got)
	}
}

func TestCloseTrackerUploadResourceReturnsCloseError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("close failed")

	err := closeTrackerUploadResource("logger", &closeCounter{closeErr: wantErr})

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected close error, got %v", err)
	}
}

func TestCloseTrackerUploadResourceReturnsPanicError(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")

	err := closeTrackerUploadResource("logger", &closeCounter{closeErr: closeErr, panicValue: "close panic"})

	if err == nil {
		t.Fatal("expected panic error")
	}
	if !strings.Contains(err.Error(), "logger close panicked") {
		t.Fatalf("expected panic context, got %v", err)
	}
	if errors.Is(err, closeErr) {
		t.Fatalf("expected panic error to take precedence over close error, got %v", err)
	}
}

func TestTrackerUploadRetryRequestUsesStoredUploadOptions(t *testing.T) {
	t.Parallel()

	job := &trackerUploadJob{
		sourcePath:     `C:\Media\Movie.mkv`,
		uploadOptions:  api.UploadOptions{Screens: 1, SkipAutoTorrent: true},
		runOptions:     runOptions{Debug: true, NoSeed: true, RunLogLevel: "debug"},
		failedTrackers: []string{"BLU"},
		ignoreDupesFor: []string{"AITHER"},
	}

	retry, err := trackerUploadRetryRequestFromJob(job)
	if err != nil {
		t.Fatalf("retry request: %v", err)
	}

	job.uploadOptions.Screens = 9
	job.failedTrackers[0] = "MUTATED"
	job.ignoreDupesFor[0] = "MUTATED"

	if retry.uploadOptions.Screens != 1 || !retry.uploadOptions.SkipAutoTorrent {
		t.Fatalf("expected stored upload options snapshot, got %#v", retry.uploadOptions)
	}
	if len(retry.failedTrackers) != 1 || retry.failedTrackers[0] != "BLU" {
		t.Fatalf("expected failed trackers snapshot, got %#v", retry.failedTrackers)
	}
	if len(retry.ignoreDupesFor) != 1 || retry.ignoreDupesFor[0] != "AITHER" {
		t.Fatalf("expected ignore dupes snapshot, got %#v", retry.ignoreDupesFor)
	}
	if !retry.runOptions.Debug || !retry.runOptions.NoSeed || retry.runOptions.RunLogLevel != "debug" {
		t.Fatalf("expected run options snapshot, got %#v", retry.runOptions)
	}
}

func TestCancelTrackerUploadDefersResourceCloseUntilWorkerExit(t *testing.T) {
	coreCloser := &closeCounterCore{}
	loggerCloser := &closeCounter{}
	job := &trackerUploadJob{
		id:     "job-1",
		core:   coreCloser,
		logger: loggerCloser,
		cancel: func() {},
	}
	app := &App{
		uploads: map[string]*trackerUploadJob{job.id: job},
	}

	if err := app.CancelTrackerUpload(job.id); err != nil {
		t.Fatalf("cancel upload: %v", err)
	}

	if got := coreCloser.count.Load(); got != 0 {
		t.Fatalf("expected cancel not to close core before worker exit, got %d", got)
	}
	if got := loggerCloser.count.Load(); got != 0 {
		t.Fatalf("expected cancel not to close logger before worker exit, got %d", got)
	}

	_ = job.closeResources()
}

func TestStopAllUploadJobsWaitsForWorkersBeforeClosingResources(t *testing.T) {
	coreCloser := &closeCounterCore{}
	loggerCloser := &closeCounter{}
	released := make(chan struct{})
	cancelCalled := make(chan struct{})
	workerFinished := make(chan struct{})
	job := &trackerUploadJob{
		id:     "job-1",
		core:   coreCloser,
		logger: loggerCloser,
	}
	app := &App{
		uploads: map[string]*trackerUploadJob{job.id: job},
	}
	app.uploadWG.Add(1)
	job.cancel = func() {
		close(cancelCalled)
		go func() {
			<-released
			app.uploadWG.Done()
			close(workerFinished)
		}()
	}

	done := make(chan struct{})
	go func() {
		app.stopAllUploadJobs()
		close(done)
	}()

	select {
	case <-cancelCalled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllUploadJobs to cancel active upload")
	}

	select {
	case <-done:
		t.Fatal("expected stopAllUploadJobs to wait for active worker")
	case <-time.After(50 * time.Millisecond):
	}
	if got := coreCloser.count.Load(); got != 0 {
		t.Fatalf("expected core to remain open while worker is active, got close count %d", got)
	}
	if got := loggerCloser.count.Load(); got != 0 {
		t.Fatalf("expected logger to remain open while worker is active, got close count %d", got)
	}

	close(released)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllUploadJobs to return after worker finishes")
	}
	<-workerFinished
	if got := coreCloser.count.Load(); got != 1 {
		t.Fatalf("expected core close after worker exit, got %d", got)
	}
	if got := loggerCloser.count.Load(); got != 1 {
		t.Fatalf("expected logger close after worker exit, got %d", got)
	}
}

func TestStopAllUploadJobsWaitsForPublishedPreStartUpload(t *testing.T) {
	coreCloser := &closeCounterCore{}
	loggerCloser := &closeCounter{}
	cancelCalled := make(chan struct{})
	job := &trackerUploadJob{
		id:     "job-1",
		core:   coreCloser,
		logger: loggerCloser,
		cancel: func() {
			close(cancelCalled)
		},
	}
	app := &App{
		uploads: map[string]*trackerUploadJob{},
	}

	app.publishTrackerUploadJob(job)
	released := atomic.Bool{}
	t.Cleanup(func() {
		if released.CompareAndSwap(false, true) {
			app.uploadWG.Done()
		}
	})

	done := make(chan struct{})
	go func() {
		app.stopAllUploadJobs()
		close(done)
	}()

	select {
	case <-cancelCalled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllUploadJobs to cancel published upload")
	}

	select {
	case <-done:
		t.Fatal("expected stopAllUploadJobs to wait for pre-start upload enrollment")
	case <-time.After(50 * time.Millisecond):
	}
	if got := coreCloser.count.Load(); got != 0 {
		t.Fatalf("expected core to remain open before pre-start upload is released, got close count %d", got)
	}
	if got := loggerCloser.count.Load(); got != 0 {
		t.Fatalf("expected logger to remain open before pre-start upload is released, got close count %d", got)
	}

	if released.CompareAndSwap(false, true) {
		app.uploadWG.Done()
	}

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllUploadJobs to return after pre-start upload release")
	}
	if got := coreCloser.count.Load(); got != 1 {
		t.Fatalf("expected core close after pre-start release, got %d", got)
	}
	if got := loggerCloser.count.Load(); got != 1 {
		t.Fatalf("expected logger close after pre-start release, got %d", got)
	}
}

func TestRunTrackerUploadJobCountsPartialErrorAndContinues(t *testing.T) {
	app := &App{}
	coreSvc := &closeCounterCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: 1},
			err:    errors.New("tracker failed after upload"),
		},
		{
			result: api.Result{UploadedCount: 2},
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU", "AITHER"})

	app.runTrackerUploadJob(context.Background(), nil, job)

	if got := job.uploadedCount; got != 3 {
		t.Fatalf("expected total uploaded count 3, got %d", got)
	}
	if got := job.states["BLU"].UploadedCount; got != 1 {
		t.Fatalf("expected failed tracker partial count 1, got %d", got)
	}
	if got := job.states["BLU"].Status; got != "failed" {
		t.Fatalf("expected partial error tracker to remain failed, got %q", got)
	}
	if got := job.states["AITHER"].UploadedCount; got != 2 {
		t.Fatalf("expected success tracker count 2, got %d", got)
	}
	if got := job.status; got != "completed_with_errors" {
		t.Fatalf("expected completed_with_errors status, got %q", got)
	}
	if len(job.failedTrackers) != 1 || job.failedTrackers[0] != "BLU" {
		t.Fatalf("expected failed tracker BLU, got %#v", job.failedTrackers)
	}
}

func TestRunDupeCheckJobSplitsGroupedPathedResultsIntoTrackerStates(t *testing.T) {
	t.Parallel()

	coreSvc := &closeCounterCore{dupeSummary: api.DupeCheckSummary{
		SourcePath: `C:\Media\Movie.mkv`,
		Results: []api.DupeCheckResult{
			{
				Tracker:  "AITHER, BLU",
				HasDupes: true,
				Status:   "completed",
				Notes:    []string{"pathed torrent match found; skipping dupe search"},
			},
			{Tracker: "RF", Status: "completed"},
		},
	}}
	job := newDupeCheckJobTestJob(coreSvc.dupeSummary.SourcePath, []string{"AITHER", "BLU", "RF"})
	job.core = coreSvc
	app := &App{}

	app.runDupeCheckJob(context.Background(), nil, job)

	if got := job.completedCount; got != 3 {
		t.Fatalf("expected all trackers completed, got %d", got)
	}
	if _, ok := job.states["AITHER, BLU"]; ok {
		t.Fatal("did not expect grouped tracker state")
	}
	for _, tracker := range []string{"AITHER", "BLU"} {
		state := job.states[tracker]
		if got := state.Status; got != "completed" {
			t.Fatalf("expected %s completed, got %q", tracker, got)
		}
		if got := state.Result.Tracker; got != tracker {
			t.Fatalf("expected %s result tracker, got %q", tracker, got)
		}
	}
}

func TestRunDupeCheckJobUsesJobCoreSnapshot(t *testing.T) {
	t.Parallel()

	jobCore := &closeCounterCore{dupeSummary: api.DupeCheckSummary{
		SourcePath: `C:\Media\Movie.mkv`,
		Results:    []api.DupeCheckResult{{Tracker: "AITHER", Status: "completed"}},
	}}
	currentCore := &closeCounterCore{dupeSummary: api.DupeCheckSummary{
		SourcePath: `C:\Media\Other.mkv`,
		Results:    []api.DupeCheckResult{{Tracker: "AITHER", Status: "failed"}},
	}}
	job := newDupeCheckJobTestJob(jobCore.dupeSummary.SourcePath, []string{"AITHER"})
	job.core = jobCore
	job.uploadOptions = api.UploadOptions{Screens: 1}
	app := &App{core: currentCore}

	app.runDupeCheckJob(context.Background(), nil, job)

	if got := jobCore.dupeCalls.Load(); got != 1 {
		t.Fatalf("expected job-owned core to run once, got %d", got)
	}
	if got := currentCore.dupeCalls.Load(); got != 0 {
		t.Fatalf("expected current runtime core not to run, got %d", got)
	}
	if got := job.states["AITHER"].Status; got != "completed" {
		t.Fatalf("expected result from job-owned core, got %q", got)
	}
}

func TestStartDupeCheckRequiresMetadataPreviewCache(t *testing.T) {
	t.Parallel()

	coreSvc := &closeCounterCore{}
	app := &App{
		core: coreSvc,
		cfg:  config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1}},
	}

	_, err := app.StartDupeCheck(`C:\Media\Example.mkv`, api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, []string{"AITHER"})
	if err == nil {
		t.Fatal("expected missing metadata preview cache to block dupe check start")
	}
	if !strings.Contains(err.Error(), "dupe check requires metadata preview") {
		t.Fatalf("expected metadata preview error, got %v", err)
	}
	if len(app.dupes) != 0 {
		t.Fatalf("expected no durable dupe job after cache miss, got %d", len(app.dupes))
	}
}

func TestStartDupeCheckUsesMetadataPreviewCache(t *testing.T) {
	t.Parallel()

	app, sourcePath := newDupeCheckStartTestApp(t)
	tmdbID := 1234567
	releaseType := "movie"
	overrides := api.ExternalIDOverrides{TMDBID: &tmdbID}
	nameOverrides := api.ReleaseNameOverrides{Type: &releaseType}
	coreSvc := &closeCounterCore{
		exportFound:  true,
		exportedMeta: api.PreparedMetadata{SourcePath: sourcePath},
		expectedExportReq: &api.Request{
			Paths:                []string{sourcePath},
			Mode:                 api.ModeGUI,
			Trackers:             []string{"AITHER"},
			ExternalIDOverrides:  overrides,
			ReleaseNameOverrides: nameOverrides,
		},
	}
	app.core = coreSvc

	jobID, err := app.StartDupeCheck(sourcePath, overrides, nameOverrides, []string{"AITHER"})
	if err != nil {
		t.Fatalf("start dupe check: %v", err)
	}
	if strings.TrimSpace(jobID) == "" {
		t.Fatal("expected job id")
	}
	if len(app.dupes) != 1 {
		t.Fatalf("expected durable dupe job, got %d", len(app.dupes))
	}
}

func TestStartDupeCheckFailsOnMetadataPreviewCacheRequestMismatch(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		expected    func(string) api.Request
		overrides   api.ExternalIDOverrides
		wantMessage string
	}{
		{
			name: "path",
			expected: func(sourcePath string) api.Request {
				return api.Request{Paths: []string{sourcePath + ".other"}, Mode: api.ModeGUI, Trackers: []string{"AITHER"}}
			},
			wantMessage: "metadata preview cache request paths",
		},
		{
			name: "tracker",
			expected: func(sourcePath string) api.Request {
				return api.Request{Paths: []string{sourcePath}, Mode: api.ModeGUI, Trackers: []string{"BLU"}}
			},
			wantMessage: "metadata preview cache request trackers",
		},
		{
			name: "external id override",
			expected: func(sourcePath string) api.Request {
				tmdbID := 7654321
				return api.Request{
					Paths:               []string{sourcePath},
					Mode:                api.ModeGUI,
					Trackers:            []string{"AITHER"},
					ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID},
				}
			},
			overrides: func() api.ExternalIDOverrides {
				tmdbID := 1234567
				return api.ExternalIDOverrides{TMDBID: &tmdbID}
			}(),
			wantMessage: "metadata preview cache request external overrides",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app, sourcePath := newDupeCheckStartTestApp(t)
			expected := tt.expected(sourcePath)
			coreSvc := &closeCounterCore{
				exportFound:       true,
				exportedMeta:      api.PreparedMetadata{SourcePath: sourcePath},
				expectedExportReq: &expected,
			}
			app.core = coreSvc

			_, err := app.StartDupeCheck(sourcePath, tt.overrides, api.ReleaseNameOverrides{}, []string{"AITHER"})
			if err == nil {
				t.Fatal("expected metadata preview cache request mismatch to fail")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected mismatch error %q, got %v", tt.wantMessage, err)
			}
			if len(app.dupes) != 0 {
				t.Fatalf("expected no durable dupe job after cache request mismatch, got %d", len(app.dupes))
			}
		})
	}
}

func TestRunTrackerUploadJobIgnoresNegativeUploadedCount(t *testing.T) {
	app := &App{}
	coreSvc := &closeCounterCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: -1},
			err:    errors.New("tracker failed after invalid count"),
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	app.runTrackerUploadJob(context.Background(), nil, job)

	if got := job.uploadedCount; got != 0 {
		t.Fatalf("expected negative count to leave total unchanged, got %d", got)
	}
	if got := job.states["BLU"].UploadedCount; got != 0 {
		t.Fatalf("expected negative count to leave tracker count unchanged, got %d", got)
	}
	if got := job.states["BLU"].Status; got != "failed" {
		t.Fatalf("expected tracker error handling to remain failed, got %q", got)
	}
	if len(job.failedTrackers) != 1 || job.failedTrackers[0] != "BLU" {
		t.Fatalf("expected failed tracker BLU, got %#v", job.failedTrackers)
	}
}

func TestRunTrackerUploadJobCountsCanceledPartialWithoutFailedTracker(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	coreSvc := &closeCounterCore{uploads: []uploadPreparedResponse{
		{
			result:       api.Result{UploadedCount: 1},
			err:          context.Canceled,
			beforeReturn: cancel,
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	app.runTrackerUploadJob(ctx, nil, job)

	if got := job.uploadedCount; got != 1 {
		t.Fatalf("expected canceled partial count 1, got %d", got)
	}
	if got := job.states["BLU"].UploadedCount; got != 1 {
		t.Fatalf("expected tracker partial count 1, got %d", got)
	}
	if got := job.status; got != "canceled" {
		t.Fatalf("expected canceled job status, got %q", got)
	}
	if len(job.failedTrackers) != 0 {
		t.Fatalf("expected no failed trackers after cancellation, got %#v", job.failedTrackers)
	}
}

func TestRunTrackerUploadJobRecoversRunUploadPreparedPanic(t *testing.T) {
	app := &App{}
	coreSvc := &closeCounterCore{uploads: []uploadPreparedResponse{
		{
			panicValue: "https://tracker.invalid/announce?api_token=secret-value",
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	app.runTrackerUploadJob(context.Background(), context.Background(), job)

	if got := job.status; got != "failed" {
		t.Fatalf("expected failed status after upload panic, got %q", got)
	}
	if !strings.Contains(job.errorMessage, "upload worker panicked") {
		t.Fatalf("expected panic error message, got %q", job.errorMessage)
	}
	if strings.Contains(job.errorMessage, "secret-value") {
		t.Fatalf("expected panic error message to redact secrets, got %q", job.errorMessage)
	}
	if got := coreSvc.count.Load(); got != 1 {
		t.Fatalf("expected core close after upload panic, got %d", got)
	}
	if job.cancel != nil {
		t.Fatal("expected upload panic to clear cancel func")
	}
}

func TestRunTrackerUploadJobContainsCleanupPanic(t *testing.T) {
	app := &App{}
	coreSvc := &closeCounterCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: 1},
		},
	}}
	coreSvc.panicValue = "https://tracker.invalid/announce?api_token=secret-value"
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	app.runTrackerUploadJob(context.Background(), context.Background(), job)

	if got := job.status; got != "failed" {
		t.Fatalf("expected failed status after cleanup panic, got %q", got)
	}
	if !strings.Contains(job.errorMessage, "core close panicked") {
		t.Fatalf("expected cleanup panic message, got %q", job.errorMessage)
	}
	if strings.Contains(job.errorMessage, "secret-value") {
		t.Fatalf("expected cleanup panic message to redact secrets, got %q", job.errorMessage)
	}
	if got := coreSvc.count.Load(); got != 1 {
		t.Fatalf("expected core close attempt once, got %d", got)
	}
}

func TestRunTrackerUploadJobSanitizesCleanupError(t *testing.T) {
	app := &App{}
	coreSvc := &closeCounterCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: 1},
		},
	}}
	coreSvc.closeErr = errors.New(`request failed for C:\path\to\Example.Release.2026.1080p-GRP: Get "https://tracker.invalid/api/torrents?api_token=secret-value"`)
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	app.runTrackerUploadJob(context.Background(), context.Background(), job)

	snapshot := buildTrackerUploadSnapshot(job)
	if snapshot.Status != "failed" {
		t.Fatalf("expected failed status after cleanup error, got %q", snapshot.Status)
	}
	if strings.Contains(snapshot.Error, "secret-value") {
		t.Fatal("expected cleanup error to redact secrets")
	}
	if strings.Contains(snapshot.Error, "Example.Release.2026.1080p-GRP") {
		t.Fatal("expected cleanup error to redact local path details")
	}
}

func newTrackerUploadJobTestJob(coreSvc api.Core, trackers []string) *trackerUploadJob {
	job := &trackerUploadJob{
		id:         "job",
		sourcePath: `C:\Media\Movie.mkv`,
		core:       coreSvc,
		trackers:   trackers,
		states:     make(map[string]TrackerUploadTrackerState, len(trackers)),
		status:     "queued",
		startedAt:  time.Now().UTC(),
	}
	for _, tracker := range trackers {
		job.states[tracker] = TrackerUploadTrackerState{Tracker: tracker, Status: "queued", Message: "queued"}
	}
	return job
}

func newDupeCheckJobTestJob(sourcePath string, trackers []string) *dupeCheckJob {
	job := &dupeCheckJob{
		id:         "dupe-job",
		sourcePath: sourcePath,
		trackers:   trackers,
		states:     make(map[string]DupeCheckTrackerState, len(trackers)),
		totalCount: len(trackers),
		status:     "queued",
		startedAt:  time.Now().UTC(),
	}
	for _, tracker := range trackers {
		job.states[tracker] = DupeCheckTrackerState{Tracker: tracker, Status: "queued", Message: "queued"}
	}
	return job
}

func newDupeCheckStartTestApp(t *testing.T) (*App, string) {
	t.Helper()

	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("example"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	repoPath := filepath.Join(tempDir, "upbrr.db")
	repo, err := db.OpenWithLogger(repoPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		_ = repo.Close()
		t.Fatalf("migrate repo: %v", err)
	}

	app := &App{
		cfg: config.Config{
			MainSettings:       config.MainSettingsConfig{DBPath: repoPath, TMDBAPI: "x"},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		},
		repo:  repo,
		dupes: make(map[string]*dupeCheckJob),
	}
	t.Cleanup(func() {
		app.stopAllDupeJobs()
		_ = repo.Close()
	})
	return app, sourcePath
}

func assertPreparedMetaExportRequest(req api.Request, expected *api.Request) error {
	if expected == nil {
		return nil
	}
	if !reflect.DeepEqual(req.Paths, expected.Paths) {
		return fmt.Errorf("metadata preview cache request paths: got %#v want %#v", req.Paths, expected.Paths)
	}
	if req.Mode != expected.Mode {
		return fmt.Errorf("metadata preview cache request mode: got %q want %q", req.Mode, expected.Mode)
	}
	if !reflect.DeepEqual(req.Trackers, expected.Trackers) {
		return fmt.Errorf("metadata preview cache request trackers: got %#v want %#v", req.Trackers, expected.Trackers)
	}
	if !reflect.DeepEqual(req.ExternalIDOverrides, expected.ExternalIDOverrides) {
		return fmt.Errorf("metadata preview cache request external overrides: got %#v want %#v", req.ExternalIDOverrides, expected.ExternalIDOverrides)
	}
	if !reflect.DeepEqual(req.ReleaseNameOverrides, expected.ReleaseNameOverrides) {
		return fmt.Errorf("metadata preview cache request name overrides: got %#v want %#v", req.ReleaseNameOverrides, expected.ReleaseNameOverrides)
	}
	return nil
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
