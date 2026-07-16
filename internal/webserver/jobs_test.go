// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	sharedjobs "github.com/autobrr/upbrr/internal/webserver/jobs"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
	"time"
)

type adapterTestCore struct {
	uploadRequest api.Request
	uploadInput   api.UploadReviewInput
	uploadResult  api.Result
	uploadErr     error
	dupeRequest   api.Request
	dupeInput     api.DuplicateCheckInput
	dupeSummary   api.DupeCheckSummary
	dupeErr       error
	exportErr     error
}

type preparedMetaTestCore struct {
	fetchReq     api.Request
	prepareInput api.PrepareInput
	closeCalls   int
}

func webTestUploadSnapshot(sourcePath string) sharedjobs.UploadExecutionSnapshot {
	return sharedjobs.UploadExecutionSnapshot{
		PreparedGeneration: 1,
		RuntimeGeneration:  1,
		Input: api.UploadReviewInput{
			Release:  api.ReleaseRef{SourcePath: sourcePath, Generation: 1},
			Trackers: []string{"EXAMPLE"},
		},
		ReviewToken: "test-review-token",
	}
}

func webTestDupeSnapshot(sourcePath string) sharedjobs.DuplicateExecutionSnapshot {
	return sharedjobs.DuplicateExecutionSnapshot{
		PreparedGeneration: 1,
		RuntimeGeneration:  1,
		Input: api.DuplicateCheckInput{
			Release:  api.ReleaseRef{SourcePath: sourcePath, Generation: 1},
			Trackers: []string{"EXAMPLE"},
		},
	}
}

func (c *preparedMetaTestCore) FetchMetadataPreview(_ context.Context, request api.Request) (api.MetadataPreview, error) {
	c.fetchReq = request
	return api.MetadataPreview{}, nil
}

func (c *preparedMetaTestCore) FetchAcceptedMetadataPreview(_ context.Context, ref api.ReleaseRef) (api.MetadataPreview, error) {
	return api.MetadataPreview{SourcePath: ref.SourcePath}, nil
}

func (c *preparedMetaTestCore) PrepareRelease(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
	c.prepareInput = input
	return api.PrepareResult{Release: api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: input.SourcePath}}}, nil
}

func (*preparedMetaTestCore) ExportReleaseSeed(context.Context, api.ReleaseRef) (preparedrelease.Seed, error) {
	return preparedrelease.Seed{}, nil
}

func (*preparedMetaTestCore) ImportReleaseSeed(context.Context, preparedrelease.Seed) (api.ReleaseRef, error) {
	return api.ReleaseRef{}, nil
}

func (*preparedMetaTestCore) SelectBlurayCandidate(context.Context, string, string) (api.MetadataPreview, error) {
	return api.MetadataPreview{}, nil
}

func (c *preparedMetaTestCore) Close() error {
	c.closeCalls++
	return nil
}

func webTestCapabilities(svc any) CoreCapabilities {
	return CoreCapabilities{
		Metadata:                   webCapabilityAs[MetadataCapability](svc),
		ReleasePreparation:         webCapabilityAs[ReleasePreparationCapability](svc),
		Selection:                  webCapabilityAs[SelectionCapability](svc),
		Preparation:                webCapabilityAs[PreparationCapability](svc),
		UploadReview:               webCapabilityAs[UploadReviewCapability](svc),
		DryRun:                     webCapabilityAs[DryRunCapability](svc),
		DuplicateExecution:         webCapabilityAs[DuplicateExecutionCapability](svc),
		UploadExecution:            webCapabilityAs[UploadExecutionCapability](svc),
		Screenshots:                webCapabilityAs[ScreenshotCapability](svc),
		HostedImages:               webCapabilityAs[HostedImageCapability](svc),
		DVD:                        webCapabilityAs[DVDCapability](svc),
		Description:                webCapabilityAs[DescriptionCapability](svc),
		Playlists:                  webCapabilityAs[PlaylistCapability](svc),
		History:                    webCapabilityAs[HistoryCapability](svc),
		PreparedGenerationTransfer: webCapabilityAs[PreparedGenerationTransfer](svc),
		DiagnosticProbe:            webCapabilityAs[DiagnosticProbeCapability](svc),
	}
}

func webCapabilityAs[T any](svc any) T {
	capability, _ := svc.(T)
	return capability
}

func (c *adapterTestCore) RunUploadPrepared(_ context.Context, request api.Request) (api.Result, error) {
	c.uploadRequest = request
	return c.uploadResult, c.uploadErr
}

func (c *adapterTestCore) RunAcceptedUpload(_ context.Context, plan api.UploadExecutionPlan) (api.Result, error) {
	c.uploadInput = plan.Input
	return c.uploadResult, c.uploadErr
}

func (c *adapterTestCore) CheckDupes(_ context.Context, request api.Request) (api.DupeCheckSummary, error) {
	c.dupeRequest = request
	return c.dupeSummary, c.dupeErr
}

func (c *adapterTestCore) CheckAcceptedDupes(_ context.Context, input api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	c.dupeInput = input
	return c.dupeSummary, c.dupeErr
}

func (*adapterTestCore) FetchPreparationPreview(context.Context, api.Request) (api.PreparationPreview, error) {
	return api.PreparationPreview{}, nil
}

func (*adapterTestCore) FetchTrackerDryRunPreview(context.Context, api.Request) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (*adapterTestCore) FetchAcceptedTrackerDryRun(context.Context, api.TrackerDryRunInput) (api.TrackerDryRunPreview, error) {
	return api.TrackerDryRunPreview{}, nil
}

func (*adapterTestCore) CaptureDVDMenus(context.Context, api.Request) (api.DVDMenuCaptureResult, error) {
	return api.DVDMenuCaptureResult{}, nil
}

func (*adapterTestCore) CaptureAcceptedDVDMenus(context.Context, api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
	return api.DVDMenuCaptureResult{}, nil
}

func (*adapterTestCore) ListDVDMenuScreenshots(context.Context, api.Request) ([]api.ScreenshotImage, error) {
	return nil, nil
}

func (*adapterTestCore) DeleteDVDMenuScreenshot(context.Context, api.Request, string) error {
	return nil
}

func (*adapterTestCore) BuildUploadReview(context.Context, api.Request) (api.UploadReview, error) {
	return api.UploadReview{}, nil
}

func (c *adapterTestCore) ReviewAcceptedUpload(context.Context, api.UploadReviewInput) (api.ReviewedUpload, error) {
	return api.ReviewedUpload{}, c.exportErr
}

func (*adapterTestCore) PrepareRelease(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
	return api.PrepareResult{Release: api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: input.SourcePath}}}, nil
}

func (*adapterTestCore) ExportReleaseSeed(context.Context, api.ReleaseRef) (preparedrelease.Seed, error) {
	return preparedrelease.Seed{}, nil
}

func (*adapterTestCore) ImportReleaseSeed(context.Context, preparedrelease.Seed) (api.ReleaseRef, error) {
	return api.ReleaseRef{}, nil
}

func TestWebUploadRunnerUsesPreparedUploadAndWebPrefix(t *testing.T) {
	t.Parallel()

	coreSvc := &adapterTestCore{uploadErr: errors.New("upload failed")}
	request := api.Request{SourcePath: "Example.Release.2026.1080p-GRP", Trackers: []string{"EXAMPLE"}}
	_, err := (webUploadRunner{
		core: coreSvc, generationTarget: coreSvc,
	}).RunUpload(context.Background(), api.UploadExecutionPlan{Input: api.UploadReviewInput{
		Release:  api.ReleaseRef{SourcePath: request.SourcePath},
		Trackers: request.Trackers,
	},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "web: ") {
		t.Fatalf("expected web-prefixed upload error, got %v", err)
	}
	if coreSvc.uploadInput.Release.SourcePath != request.SourcePath || coreSvc.uploadInput.Trackers[0] != request.Trackers[0] {
		t.Fatalf("prepared upload input mismatch: %#v", coreSvc.uploadInput)
	}
}

func TestWebDupeRunnerPreservesRequest(t *testing.T) {
	t.Parallel()

	coreSvc := &adapterTestCore{dupeSummary: api.DupeCheckSummary{SourcePath: "Example.Release.2026.1080p-GRP"}}
	request := api.Request{
		SourcePath:           "Example.Release.2026.1080p-GRP",
		Trackers:             []string{"EXAMPLE"},
		ExternalIDOverrides:  api.ExternalIDOverrides{},
		ReleaseNameOverrides: api.ReleaseNameOverrides{},
	}
	summary, err := (webDupeRunner{
		core: coreSvc, generationTarget: coreSvc,
	}).CheckDupes(context.Background(), api.DuplicateCheckInput{
		Release:  api.ReleaseRef{SourcePath: request.SourcePath},
		Trackers: request.Trackers,
	})
	if err != nil {
		t.Fatalf("check dupes: %v", err)
	}
	if summary.SourcePath != coreSvc.dupeInput.Release.SourcePath {
		t.Fatalf("dupe input mismatch: %#v", coreSvc.dupeInput)
	}
}

func TestStartTrackerUploadRejectsPartialCapabilityBundle(t *testing.T) {
	t.Parallel()

	backend := &Backend{
		capabilities: CoreCapabilities{UploadExecution: &adapterTestCore{}},
	}
	_, err := backend.ReviewTrackerUpload(
		context.Background(),
		"session-1",
		api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 1},
		[]string{"EXAMPLE"},
		nil,
		nil,
		nil,
		false,
		false,
		"",
	)
	if !errors.Is(err, ErrPreparedGenerationUnavailable) {
		t.Fatalf("expected missing transfer capability error, got %v", err)
	}
}

func TestWebStartTrackerUploadExportsBeforePerRunSetup(t *testing.T) {
	t.Parallel()

	exportErr := errors.New("prepared authorization rejected")
	coreSvc := &adapterTestCore{exportErr: exportErr}
	backend := &Backend{capabilities: webTestCapabilities(coreSvc)}
	t.Cleanup(func() {
		if backend.jobEngine != nil {
			backend.jobEngine.Close()
		}
	})
	_, err := backend.ReviewTrackerUpload(
		context.Background(),
		"session-1",
		api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 1},
		[]string{"EXAMPLE"},
		nil,
		nil,
		nil,
		false,
		false,
		"",
	)
	if !errors.Is(err, exportErr) {
		t.Fatalf("expected export error before per-run setup, got %v", err)
	}
}

func TestWebDupeRunnerPreservesCoreErrorText(t *testing.T) {
	t.Parallel()

	want := "provider rejected duplicate check"
	coreSvc := &adapterTestCore{dupeErr: errors.New(want)}
	_, err := (webDupeRunner{core: coreSvc, generationTarget: coreSvc}).CheckDupes(
		context.Background(), api.DuplicateCheckInput{},
	)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want exact %q", err, want)
	}
}

type successfulUploadRunner struct{}

func (successfulUploadRunner) RunUpload(context.Context, api.UploadExecutionPlan) (api.Result, error) {
	return api.Result{UploadedCount: 1}, nil
}

type successfulDupeRunner struct{}

func (successfulDupeRunner) CheckDupes(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, nil
}

func TestWebJobFacadesPreserveLegacyOwnershipErrors(t *testing.T) {
	t.Parallel()

	backend := &Backend{jobEngine: sharedjobs.New(nil, sharedjobs.Config{})}
	t.Cleanup(backend.jobEngine.Close)

	owner, err := backend.ensureJobOwner("owner-a")
	if err != nil {
		t.Fatalf("ensure owner: %v", err)
	}
	sourcePath := "Example.Release.2026.1080p-GRP"
	dupeID, err := backend.jobEngine.StartDupe(context.Background(), owner, sharedjobs.DupeSpec{
		CorrelationID: "owner-dupe",
		Snapshot: webTestDupeSnapshot(sourcePath),
		Runner:   successfulDupeRunner{},
	})
	if err != nil {
		t.Fatalf("start dupe: %v", err)
	}
	uploadID, err := backend.jobEngine.StartUpload(context.Background(), owner, sharedjobs.UploadSpec{
		CorrelationID: "owner-upload",
		Snapshot: webTestUploadSnapshot(sourcePath),
		Runner:   successfulUploadRunner{},
	})
	if err != nil {
		t.Fatalf("start upload: %v", err)
	}

	assertExactError(t, func() error {
		_, snapshotErr := backend.GetDupeCheckSnapshot("owner-b", dupeID)
		return snapshotErr
	}, "dupe job not found")
	assertExactError(t, func() error { return backend.CancelDupeCheck("owner-b", dupeID) }, "dupe job not found")
	assertExactError(t, func() error {
		_, snapshotErr := backend.GetDupeCheckSnapshot("owner-a", "unknown")
		return snapshotErr
	}, "dupe job not found")

	assertExactError(t, func() error {
		_, snapshotErr := backend.GetTrackerUploadSnapshot("owner-b", uploadID)
		return snapshotErr
	}, "upload job not found")
	assertExactError(t, func() error { return backend.CancelTrackerUpload("owner-b", uploadID) }, "upload job not found")
	assertExactError(t, func() error {
		_, retryErr := backend.RetryFailedTrackerUpload("owner-b", uploadID, "foreign-retry")
		return retryErr
	}, "upload job not found")
	assertExactError(t, func() error {
		_, snapshotErr := backend.GetTrackerUploadSnapshot("owner-a", "unknown")
		return snapshotErr
	}, "upload job not found")

	deadline := time.Now().Add(time.Second)
	for {
		snapshot, snapshotErr := backend.GetTrackerUploadSnapshot("owner-a", uploadID)
		if snapshotErr != nil {
			t.Fatalf("get upload snapshot: %v", snapshotErr)
		}
		if snapshot.Status == sharedjobs.StatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload did not complete: %#v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
	assertExactError(t, func() error {
		_, retryErr := backend.RetryFailedTrackerUpload("owner-a", uploadID, "owner-retry")
		return retryErr
	}, "no failed trackers to retry")
}

func assertExactError(t *testing.T, call func() error, want string) {
	t.Helper()
	if err := call(); err == nil || err.Error() != want {
		t.Fatalf("error = %v, want exact %q", err, want)
	}
}

func TestWebJobSinkUsesSessionAndExistingTopics(t *testing.T) {
	t.Parallel()

	hub := newEventHub()
	events, stop := hub.Subscribe("session-a")
	defer stop()

	sink := webJobSink{hub: hub}
	sink.EmitDupe("session-a", sharedjobs.DupeCheckSnapshot{JobID: "dupe-1"})
	sink.EmitUpload("session-a", sharedjobs.TrackerUploadSnapshot{JobID: "upload-1"})

	select {
	case event := <-events:
		if event.Name != "dupe:job:dupe-1" {
			t.Fatalf("unexpected dupe topic %q", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("expected dupe event on existing topic")
	}
	select {
	case event := <-events:
		if event.Name != "jobs:update" {
			t.Fatalf("unexpected registry topic %q", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("expected dupe registry event")
	}
	select {
	case event := <-events:
		if event.Name != "upload:job:upload-1" {
			t.Fatalf("unexpected upload topic %q", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("expected upload event on existing topic")
	}
	select {
	case event := <-events:
		if event.Name != "jobs:update" {
			t.Fatalf("unexpected registry topic %q", event.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("expected upload registry event")
	}
}

type blockingUploadRunner struct {
	started  chan<- struct{}
	canceled chan<- struct{}
	release  <-chan struct{}
}

func (r blockingUploadRunner) RunUpload(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
	if r.started != nil {
		r.started <- struct{}{}
	}
	<-ctx.Done()
	select {
	case r.canceled <- struct{}{}:
	default:
	}
	<-r.release
	return api.Result{}, fmt.Errorf("blocking upload: %w", ctx.Err())
}

type blockingDupeRunner struct {
	started  chan<- struct{}
	canceled chan<- struct{}
	release  <-chan struct{}
}

func (r blockingDupeRunner) CheckDupes(ctx context.Context, _ api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	if r.started != nil {
		r.started <- struct{}{}
	}
	<-ctx.Done()
	select {
	case r.canceled <- struct{}{}:
	default:
	}
	<-r.release
	return api.DupeCheckSummary{}, fmt.Errorf("blocking dupe: %w", ctx.Err())
}

func installBlockingUploadJob(
	t *testing.T,
	backend *Backend,
	ownerID string,
	canceled chan<- struct{},
	release <-chan struct{},
) string {
	t.Helper()
	if backend.jobEngine == nil {
		backend.jobEngine = sharedjobs.New(webJobSink{hub: backend.hub}, sharedjobs.Config{})
	}
	owner, err := backend.ensureJobOwner(ownerID)
	if err != nil {
		t.Fatalf("ensure owner: %v", err)
	}
	jobID, err := backend.jobEngine.StartUpload(context.Background(), owner, sharedjobs.UploadSpec{
		CorrelationID: "blocking-upload",
		Snapshot: webTestUploadSnapshot("Example.Release.2026.1080p-GRP"),
		Runner:   blockingUploadRunner{canceled: canceled, release: release},
	})
	if err != nil {
		t.Fatalf("start blocking upload: %v", err)
	}
	return jobID
}

func installBlockingDupeJob(
	t *testing.T,
	backend *Backend,
	ownerID string,
	canceled chan<- struct{},
	release <-chan struct{},
) string {
	t.Helper()
	if backend.jobEngine == nil {
		backend.jobEngine = sharedjobs.New(webJobSink{hub: backend.hub}, sharedjobs.Config{})
	}
	owner, err := backend.ensureJobOwner(ownerID)
	if err != nil {
		t.Fatalf("ensure owner: %v", err)
	}
	jobID, err := backend.jobEngine.StartDupe(context.Background(), owner, sharedjobs.DupeSpec{
		CorrelationID: "blocking-dupe",
		Snapshot: webTestDupeSnapshot("Example.Release.2026.1080p-GRP"),
		Runner:   blockingDupeRunner{canceled: canceled, release: release},
	})
	if err != nil {
		t.Fatalf("start blocking dupe: %v", err)
	}
	return jobID
}
