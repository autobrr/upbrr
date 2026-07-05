// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/guishared"
	"github.com/autobrr/upbrr/pkg/api"
)

type preparedMetaTestCore struct {
	exportedMeta api.PreparedMetadata
	exportFound  bool
	exportErr    error
	importedMeta api.PreparedMetadata
	importedReq  api.Request
	fetchReq     api.Request
	dupeSummary  api.DupeCheckSummary
	uploads      []uploadPreparedResponse
	uploadCalls  int
}

type uploadPreparedResponse struct {
	result       api.Result
	err          error
	beforeReturn func()
}

func (c *preparedMetaTestCore) RunUpload(context.Context, api.Request) (api.Result, error) {
	return api.Result{}, nil
}

func (c *preparedMetaTestCore) RunUploadPrepared(context.Context, api.Request) (api.Result, error) {
	if c.uploadCalls < len(c.uploads) {
		response := c.uploads[c.uploadCalls]
		c.uploadCalls++
		if response.beforeReturn != nil {
			response.beforeReturn()
		}
		return response.result, response.err
	}
	return api.Result{}, nil
}

func (c *preparedMetaTestCore) FetchMetadataPreview(_ context.Context, req api.Request) (api.MetadataPreview, error) {
	c.fetchReq = req
	return api.MetadataPreview{}, nil
}

func (c *preparedMetaTestCore) FetchDescriptionBuilderPreview(context.Context, api.Request) (api.DescriptionBuilderPreview, error) {
	return api.DescriptionBuilderPreview{}, nil
}

func (c *preparedMetaTestCore) FetchPreparationPreview(context.Context, api.Request) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (c *preparedMetaTestCore) FetchTrackerDryRunPreview(context.Context, api.Request) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (c *preparedMetaTestCore) CheckDupes(context.Context, api.Request) (api.DupeCheckSummary, error) {
	return c.dupeSummary, nil
}

func (c *preparedMetaTestCore) BuildUploadReview(context.Context, api.Request) (api.UploadReview, error) {
	return api.UploadReview{}, nil
}

func (c *preparedMetaTestCore) FetchScreenshotPlan(context.Context, api.Request) (api.ScreenshotPlan, error) {
	return api.ScreenshotPlan{}, nil
}

func (c *preparedMetaTestCore) GenerateScreenshots(context.Context, api.Request, []api.ScreenshotSelection, api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	return api.ScreenshotResult{}, nil
}

func (c *preparedMetaTestCore) PreviewScreenshotFrame(context.Context, api.Request, float64) (api.ScreenshotPreview, error) {
	return api.ScreenshotPreview{}, nil
}

func (c *preparedMetaTestCore) DeleteScreenshot(context.Context, api.Request, string) error {
	return nil
}

func (c *preparedMetaTestCore) DeleteTrackerImageURL(context.Context, api.Request, string) error {
	return nil
}

func (c *preparedMetaTestCore) SaveFinalScreenshotSelections(context.Context, api.Request, []api.ScreenshotImage) error {
	return nil
}

func (c *preparedMetaTestCore) ImportMenuImages(context.Context, api.Request, []string) error {
	return nil
}

func (c *preparedMetaTestCore) ListUploadCandidates(context.Context, api.Request) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) ListUploadedImages(context.Context, api.Request) ([]api.UploadedImageLink, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) UploadImages(context.Context, api.Request, string, []api.ScreenshotImage) (api.UploadImagesResult, error) {
	return api.UploadImagesResult{}, nil
}

func (c *preparedMetaTestCore) DeleteUploadedImage(context.Context, api.Request, string, string) error {
	return nil
}

func (c *preparedMetaTestCore) DiscoverPlaylists(context.Context, string) ([]api.PlaylistInfo, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return nil
}

func (c *preparedMetaTestCore) LoadPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, nil
}

func (c *preparedMetaTestCore) ListHistory(context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (c *preparedMetaTestCore) GetHistoryOverview(context.Context, string) (api.HistoryOverview, error) {
	return api.HistoryOverview{}, nil
}

func (c *preparedMetaTestCore) DeleteHistoryRelease(context.Context, string) error {
	return nil
}

func (c *preparedMetaTestCore) DeleteAllHistoryReleases(context.Context) (int, error) {
	return 0, nil
}

func (c *preparedMetaTestCore) RenderDescription(context.Context, string) (string, error) {
	return "", nil
}

func (c *preparedMetaTestCore) FetchDescriptionBuilderGroupPreview(context.Context, api.Request) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *preparedMetaTestCore) SaveDescriptionOverride(context.Context, api.Request, string) (api.DescriptionBuilderGroup, error) {
	return api.DescriptionBuilderGroup{}, nil
}

func (c *preparedMetaTestCore) Close() error {
	return nil
}

func (c *preparedMetaTestCore) ExportGUICachedPreparedMeta(context.Context, api.Request) (api.PreparedMetadata, bool, error) {
	return c.exportedMeta, c.exportFound, c.exportErr
}

func (c *preparedMetaTestCore) ImportPreparedMetadataForGUI(_ context.Context, req api.Request, meta api.PreparedMetadata) error {
	c.importedReq = req
	c.importedMeta = meta
	return nil
}

func TestPruneCompletedDupeJobsLockedKeepsNewestCompleted(t *testing.T) {
	backend := &Backend{
		dupes: make(map[string]*dupeCheckJob),
	}

	active := &dupeCheckJob{id: "active", status: "running"}
	backend.dupes[active.id] = active

	now := time.Now().UTC()
	for idx := range 3 {
		id := fmt.Sprintf("dupe-%d", idx)
		backend.dupes[id] = &dupeCheckJob{
			id:         id,
			status:     "completed",
			finishedAt: now.Add(time.Duration(idx) * time.Minute),
		}
	}

	backend.pruneCompletedDupeJobsLocked(2)

	if _, ok := backend.dupes["dupe-0"]; ok {
		t.Fatal("expected oldest completed dupe job to be pruned")
	}
	if _, ok := backend.dupes["dupe-1"]; !ok {
		t.Fatal("expected newer completed dupe job to remain")
	}
	if _, ok := backend.dupes["dupe-2"]; !ok {
		t.Fatal("expected newest completed dupe job to remain")
	}
	if _, ok := backend.dupes[active.id]; !ok {
		t.Fatal("expected active dupe job to remain")
	}
}

func TestStartDupeCheckRequiresMetadataPreviewCache(t *testing.T) {
	t.Parallel()

	coreSvc := &preparedMetaTestCore{}
	backend := &Backend{
		core:  coreSvc,
		dupes: make(map[string]*dupeCheckJob),
	}

	_, err := backend.StartDupeCheck("session", `C:\Media\Example.mkv`, api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, []string{"AITHER"})
	if err == nil {
		t.Fatal("expected missing metadata preview cache to block dupe check start")
	}
	if !strings.Contains(err.Error(), "dupe check requires metadata preview") {
		t.Fatalf("expected metadata preview error, got %v", err)
	}
	if len(backend.dupes) != 0 {
		t.Fatalf("expected no durable dupe job after cache miss, got %d", len(backend.dupes))
	}
}

func TestStartDupeCheckUsesMetadataPreviewCache(t *testing.T) {
	t.Parallel()

	coreSvc := &preparedMetaTestCore{exportFound: true}
	backend := &Backend{
		core:  coreSvc,
		dupes: make(map[string]*dupeCheckJob),
		hub:   newEventHub(),
	}

	jobID, err := backend.StartDupeCheck("session", `C:\Media\Example.mkv`, api.ExternalIDOverrides{}, api.ReleaseNameOverrides{}, []string{"AITHER"})
	if err != nil {
		t.Fatalf("start dupe check: %v", err)
	}
	if strings.TrimSpace(jobID) == "" {
		t.Fatal("expected job id")
	}
	if len(backend.dupes) != 1 {
		t.Fatalf("expected durable dupe job, got %d", len(backend.dupes))
	}
}

func TestPruneCompletedUploadJobsLockedKeepsNewestCompleted(t *testing.T) {
	backend := &Backend{
		uploads: make(map[string]*trackerUploadJob),
	}

	active := &trackerUploadJob{id: "active", status: "running"}
	backend.uploads[active.id] = active

	now := time.Now().UTC()
	for idx := range 3 {
		id := fmt.Sprintf("upload-%d", idx)
		backend.uploads[id] = &trackerUploadJob{
			id:         id,
			status:     "completed",
			finishedAt: now.Add(time.Duration(idx) * time.Minute),
		}
	}

	backend.pruneCompletedUploadJobsLocked(2)

	if _, ok := backend.uploads["upload-0"]; ok {
		t.Fatal("expected oldest completed upload job to be pruned")
	}
	if _, ok := backend.uploads["upload-1"]; !ok {
		t.Fatal("expected newer completed upload job to remain")
	}
	if _, ok := backend.uploads["upload-2"]; !ok {
		t.Fatal("expected newest completed upload job to remain")
	}
	if _, ok := backend.uploads[active.id]; !ok {
		t.Fatal("expected active upload job to remain")
	}
}

func TestDupeJobAccessRequiresOwningSession(t *testing.T) {
	backend := &Backend{
		dupes: map[string]*dupeCheckJob{
			"job-1": {
				sessionID:  "session-a",
				id:         "job-1",
				sourcePath: `C:\Media\Movie.mkv`,
				status:     "running",
				states:     map[string]DupeCheckTrackerState{},
				startedAt:  time.Now().UTC(),
			},
		},
	}

	if _, err := backend.GetDupeCheckSnapshot("session-b", "job-1"); err == nil {
		t.Fatal("expected foreign session snapshot read to be rejected")
	}
	if err := backend.CancelDupeCheck("session-b", "job-1"); err == nil {
		t.Fatal("expected foreign session cancel to be rejected")
	}
	if _, err := backend.GetDupeCheckSnapshot("session-a", "job-1"); err != nil {
		t.Fatalf("expected owning session snapshot read to succeed: %v", err)
	}
}

func TestTrackerUploadJobAccessRequiresOwningSession(t *testing.T) {
	backend := &Backend{
		uploads: map[string]*trackerUploadJob{
			"job-1": {
				sessionID:  "session-a",
				id:         "job-1",
				sourcePath: `C:\Media\Movie.mkv`,
				status:     "running",
				states:     map[string]TrackerUploadTrackerState{},
				startedAt:  time.Now().UTC(),
			},
		},
	}

	if _, err := backend.GetTrackerUploadSnapshot("session-b", "job-1"); err == nil {
		t.Fatal("expected foreign session snapshot read to be rejected")
	}
	if err := backend.CancelTrackerUpload("session-b", "job-1"); err == nil {
		t.Fatal("expected foreign session cancel to be rejected")
	}
	if _, err := backend.RetryFailedTrackerUpload("session-b", "job-1"); err == nil {
		t.Fatal("expected foreign session retry to be rejected")
	}
	if _, err := backend.GetTrackerUploadSnapshot("session-a", "job-1"); err != nil {
		t.Fatalf("expected owning session snapshot read to succeed: %v", err)
	}
}

func TestTrackerUploadRetryRequestUsesStoredUploadOptions(t *testing.T) {
	job := &trackerUploadJob{
		sessionID:      "session-a",
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

	if retry.sessionID != "session-a" {
		t.Fatalf("expected retry session snapshot, got %q", retry.sessionID)
	}
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

func TestRunTrackerUploadJobCountsPartialErrorAndContinues(t *testing.T) {
	backend := &Backend{hub: newEventHub()}
	coreSvc := &preparedMetaTestCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: 1},
			err:    errors.New("tracker failed after upload"),
		},
		{
			result: api.Result{UploadedCount: 2},
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU", "AITHER"})

	backend.uploadWG.Add(1)
	backend.runTrackerUploadJob(context.Background(), job)

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

	coreSvc := &preparedMetaTestCore{dupeSummary: api.DupeCheckSummary{
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
	job := newDupeCheckJobTestJob("session-a", coreSvc.dupeSummary.SourcePath, []string{"AITHER", "BLU", "RF"})
	backend := &Backend{core: coreSvc, hub: newEventHub()}
	backend.dupeWG.Add(1)

	backend.runDupeCheckJob(context.Background(), job)

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

func TestRunTrackerUploadJobIgnoresNegativeUploadedCount(t *testing.T) {
	backend := &Backend{hub: newEventHub()}
	coreSvc := &preparedMetaTestCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: -1},
			err:    errors.New("tracker failed after invalid count"),
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	backend.uploadWG.Add(1)
	backend.runTrackerUploadJob(context.Background(), job)

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
	backend := &Backend{hub: newEventHub()}
	ctx, cancel := context.WithCancel(context.Background())
	coreSvc := &preparedMetaTestCore{uploads: []uploadPreparedResponse{
		{
			result:       api.Result{UploadedCount: 1},
			err:          context.Canceled,
			beforeReturn: cancel,
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU"})

	backend.uploadWG.Add(1)
	backend.runTrackerUploadJob(ctx, job)

	if got := job.uploadedCount; got != 1 {
		t.Fatalf("expected canceled partial count 1, got %d", got)
	}
	if got := job.states["BLU"].UploadedCount; got != 1 {
		t.Fatalf("expected tracker partial count 1, got %d", got)
	}
	if got := job.states["BLU"].Status; got != "canceled" {
		t.Fatalf("expected canceled tracker status, got %q", got)
	}
	if got := job.states["BLU"].Message; got != "canceled" {
		t.Fatalf("expected canceled tracker message, got %q", got)
	}
	if got := job.states["BLU"].FinishedAt; got == "" {
		t.Fatal("expected canceled tracker finished time")
	}
	assertSnapshotHasNoActiveTrackerStates(t, buildTrackerUploadSnapshot(job))
	if got := job.status; got != "canceled" {
		t.Fatalf("expected canceled job status, got %q", got)
	}
	if len(job.failedTrackers) != 0 {
		t.Fatalf("expected no failed trackers after cancellation, got %#v", job.failedTrackers)
	}
}

func TestRunTrackerUploadJobCancelsQueuedTrackersBeforeFirstUpload(t *testing.T) {
	backend := &Backend{hub: newEventHub()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coreSvc := &preparedMetaTestCore{}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU", "AITHER"})

	backend.uploadWG.Add(1)
	backend.runTrackerUploadJob(ctx, job)

	if got := coreSvc.uploadCalls; got != 0 {
		t.Fatalf("expected no upload calls after pre-start cancellation, got %d", got)
	}
	if got := job.status; got != "canceled" {
		t.Fatalf("expected canceled job status, got %q", got)
	}
	for _, tracker := range job.trackers {
		state := job.states[tracker]
		if state.Status != "canceled" {
			t.Fatalf("expected %s status canceled, got %q", tracker, state.Status)
		}
		if state.Message != "canceled" {
			t.Fatalf("expected %s message canceled, got %q", tracker, state.Message)
		}
		if state.FinishedAt == "" {
			t.Fatalf("expected %s finished time", tracker)
		}
	}
	assertSnapshotHasNoActiveTrackerStates(t, buildTrackerUploadSnapshot(job))
}

func TestRunTrackerUploadJobCancelsRunningAndQueuedTrackersWithoutOverwritingSuccess(t *testing.T) {
	backend := &Backend{hub: newEventHub()}
	ctx, cancel := context.WithCancel(context.Background())
	coreSvc := &preparedMetaTestCore{uploads: []uploadPreparedResponse{
		{
			result: api.Result{UploadedCount: 1},
		},
		{
			err:          context.Canceled,
			beforeReturn: cancel,
		},
	}}
	job := newTrackerUploadJobTestJob(coreSvc, []string{"BLU", "AITHER", "BHD"})

	backend.uploadWG.Add(1)
	backend.runTrackerUploadJob(ctx, job)

	if got := job.uploadedCount; got != 1 {
		t.Fatalf("expected total uploaded count 1, got %d", got)
	}
	if got := job.states["BLU"].Status; got != "success" {
		t.Fatalf("expected completed tracker success to remain unchanged, got %q", got)
	}
	if got := job.states["AITHER"].Status; got != "canceled" {
		t.Fatalf("expected running tracker canceled, got %q", got)
	}
	if got := job.states["BHD"].Status; got != "canceled" {
		t.Fatalf("expected queued tracker canceled, got %q", got)
	}
	assertSnapshotHasNoActiveTrackerStates(t, buildTrackerUploadSnapshot(job))
	if got := job.status; got != "canceled" {
		t.Fatalf("expected canceled job status, got %q", got)
	}
	if len(job.failedTrackers) != 0 {
		t.Fatalf("expected no failed trackers after cancellation, got %#v", job.failedTrackers)
	}
}

func TestApplyTrackerUploadProgressThrottlesSmallHashRateBurst(t *testing.T) {
	hub := newEventHub()
	ch, unsubscribe := hub.Subscribe("session")
	defer unsubscribe()
	backend := &Backend{hub: hub}
	job := newTrackerUploadProgressTestJob()
	job.lastSnapshotEmit = time.Now()
	job.snapshotThrottle = time.Hour
	job.currentTaskStatus = "running"
	job.currentPercent = 40
	job.currentHashRateMiB = 10

	backend.applyTrackerUploadProgress(job, api.UploadProgressUpdate{
		Tracker:     "BLU",
		Status:      "running",
		Percent:     40,
		HashRateMiB: 10.25,
	})

	select {
	case event := <-ch:
		t.Fatalf("expected throttled hash-rate-only update, got event %q", event.Name)
	default:
	}
}

func TestApplyTrackerUploadProgressEmitsMeaningfulChangeWithinThrottle(t *testing.T) {
	tests := []struct {
		name   string
		update api.UploadProgressUpdate
	}{
		{
			name: "percent changed",
			update: api.UploadProgressUpdate{
				Tracker: "BLU",
				Status:  "running",
				Percent: 41,
			},
		},
		{
			name: "status changed",
			update: api.UploadProgressUpdate{
				Tracker: "BLU",
				Status:  "completed",
				Percent: 40,
			},
		},
		{
			name: "hash rate changed beyond threshold",
			update: api.UploadProgressUpdate{
				Tracker:     "BLU",
				Status:      "running",
				Percent:     40,
				HashRateMiB: 12,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := newEventHub()
			ch, unsubscribe := hub.Subscribe("session")
			defer unsubscribe()
			backend := &Backend{hub: hub}
			job := newTrackerUploadProgressTestJob()
			job.lastSnapshotEmit = time.Now()
			job.snapshotThrottle = time.Hour
			job.currentTaskStatus = "running"
			job.currentPercent = 40
			job.currentHashRateMiB = 10

			backend.applyTrackerUploadProgress(job, tt.update)

			select {
			case event := <-ch:
				if event.Name != "upload:job:job" {
					t.Fatalf("unexpected event name: %q", event.Name)
				}
			default:
				t.Fatal("expected meaningful progress change to emit immediately")
			}
		})
	}
}

func TestApplyTrackerUploadProgressUpdatesOnlyNamedTrackerForMultiTrackerJobs(t *testing.T) {
	hub := newEventHub()
	backend := &Backend{hub: hub}
	job := &trackerUploadJob{
		sessionID: "session",
		id:        "job",
		trackers:  []string{"BLU", "AITHER"},
		states: map[string]TrackerUploadTrackerState{
			"BLU":    {Tracker: "BLU", Status: "running", Message: "queued"},
			"AITHER": {Tracker: "AITHER", Status: "running", Message: "queued"},
		},
		startedAt: time.Now().UTC(),
	}

	backend.applyTrackerUploadProgress(job, api.UploadProgressUpdate{
		Tracker: "BLU",
		Task:    "tracker_upload",
		Status:  "running",
		Message: "Uploading to tracker",
		Percent: 40,
	})

	if got := job.states["BLU"].Task; got != "tracker_upload" {
		t.Fatalf("expected BLU task to update, got %q", got)
	}
	if got := job.states["BLU"].Message; got != "Uploading to tracker" {
		t.Fatalf("expected BLU message to update, got %q", got)
	}
	if got := job.states["AITHER"].Task; got != "" {
		t.Fatalf("expected AITHER task to remain untouched, got %q", got)
	}
	if got := job.states["AITHER"].Message; got != "queued" {
		t.Fatalf("expected AITHER message to remain queued, got %q", got)
	}
}

func newTrackerUploadProgressTestJob() *trackerUploadJob {
	return &trackerUploadJob{
		sessionID: "session",
		id:        "job",
		trackers:  []string{"BLU"},
		states: map[string]TrackerUploadTrackerState{
			"BLU": {Tracker: "BLU", Status: "running", Message: "uploading"},
		},
		startedAt: time.Now().UTC(),
	}
}

func newTrackerUploadJobTestJob(coreSvc api.Core, trackers []string) *trackerUploadJob {
	job := &trackerUploadJob{
		sessionID:  "session",
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

func newDupeCheckJobTestJob(sessionID string, sourcePath string, trackers []string) *dupeCheckJob {
	job := &dupeCheckJob{
		sessionID:  sessionID,
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

func assertSnapshotHasNoActiveTrackerStates(t *testing.T, snapshot TrackerUploadSnapshot) {
	t.Helper()
	if snapshot.Status != "canceled" {
		t.Fatalf("expected canceled snapshot status, got %q", snapshot.Status)
	}
	for _, tracker := range snapshot.Trackers {
		if tracker.Status == "queued" || tracker.Status == "running" {
			t.Fatalf("expected snapshot tracker %s terminal, got %q", tracker.Tracker, tracker.Status)
		}
	}
}

func TestSeedRunCorePreparedMetaCopiesPreparedMetadata(t *testing.T) {
	source := &preparedMetaTestCore{
		exportFound:  true,
		exportedMeta: api.PreparedMetadata{SourcePath: "C:\\releases\\movie.mkv"},
	}
	target := &preparedMetaTestCore{}
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
	source := &preparedMetaTestCore{}
	target := &preparedMetaTestCore{}

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
