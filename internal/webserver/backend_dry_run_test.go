// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	sharedjobs "github.com/autobrr/upbrr/internal/webserver/jobs"
	"github.com/autobrr/upbrr/pkg/api"
)

type dryRunCaptureCapability struct {
	plan api.TrackerDryRunPlan
}

func (c *dryRunCaptureCapability) RunAcceptedTrackerDryRun(ctx context.Context, plan api.TrackerDryRunPlan) (api.TrackerDryRunPreview, error) {
	if err := ctx.Err(); err != nil {
		return api.TrackerDryRunPreview{}, fmt.Errorf("captured dry run canceled: %w", err)
	}
	c.plan = plan
	return api.TrackerDryRunPreview{SourcePath: plan.Input.Release.SourcePath}, nil
}

type completedDryRunDupeRunner struct{}

func (completedDryRunDupeRunner) CheckDupes(_ context.Context, input api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	results := make([]api.DupeCheckResult, 0, len(input.Trackers))
	for _, tracker := range input.Trackers {
		results = append(results, api.DupeCheckResult{
Tracker: tracker,
 Status: "completed",
 Notes: []string{"retained"},
})
	}
	return api.DupeCheckSummary{SourcePath: input.Release.SourcePath, Results: results}, nil
}

type failedDryRunDupeRunner struct{}

func (failedDryRunDupeRunner) CheckDupes(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, errors.New("duplicate service unavailable")
}

type blockingDryRunDupeRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r blockingDryRunDupeRunner) CheckDupes(ctx context.Context, _ api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	close(r.started)
	select {
	case <-ctx.Done():
		return api.DupeCheckSummary{}, fmt.Errorf("blocking duplicate check canceled: %w", ctx.Err())
	case <-r.release:
		return api.DupeCheckSummary{}, nil
	}
}

func TestFetchTrackerDryRunUsesActiveCapabilityAndRetainedDupeEvidence(t *testing.T) {
	t.Parallel()

	capture := &dryRunCaptureCapability{}
	backend := &Backend{
		cfg:               config.Config{Logging: config.LoggingConfig{Level: "info"}},
		capabilities:      CoreCapabilities{DryRun: capture},
		runtimeGeneration: 41,
		hub:               newEventHub(),
		jobEngine:         sharedjobs.New(nil, sharedjobs.Config{}),
		jobOwners:         make(map[string]*sharedjobs.OwnerHandle),
	}
	t.Cleanup(backend.jobEngine.Close)

	release := api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 7}
	jobID := startCompletedDryRunDupeJob(t, backend, "session-a", release, 41, []string{"AITHER"})
	preview, err := backend.FetchTrackerDryRun(
		context.Background(),
		"session-a",
		jobID,
		release,
		[]string{"AITHER"},
		nil,
		nil,
		nil,
		true,
		"trace",
	)
	if err != nil {
		t.Fatalf("fetch tracker dry run: %v", err)
	}
	if preview.SourcePath != release.SourcePath || capture.plan.Input.Release != release {
		t.Fatalf("preview=%#v plan=%#v", preview, capture.plan)
	}
	if capture.plan.Input.Options.RunLogLevel != "trace" || !capture.plan.Input.Options.NoSeed {
		t.Fatalf("run options = %#v", capture.plan.Input.Options)
	}
	if capture.plan.Duplicate.Release != release || len(capture.plan.Duplicate.Results) != 1 || capture.plan.Duplicate.Results[0].Tracker != "AITHER" {
		t.Fatalf("duplicate evidence = %#v", capture.plan.Duplicate)
	}
	if backend.capabilities.PreparedGenerationTransfer != nil {
		t.Fatal("test unexpectedly installed generation transfer")
	}
}

func TestFetchTrackerDryRunRejectsInvalidDupeJobLineage(t *testing.T) {
	t.Parallel()

	release := api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 7}
	tests := []struct {
		name              string
		owner             string
		requestOwner      string
		requestJob        string
		jobRelease        api.ReleaseRef
		jobRuntime        uint64
		jobTrackers       []string
		requestTrackers   []string
		wantFailure       api.OperationFailureCode
	}{
		{
			name:            "missing job",
			owner:           "session-a",
			requestOwner:    "session-a",
			requestJob:      "missing-job",
			jobRelease:      release,
			jobRuntime:      41,
			jobTrackers:     []string{"AITHER"},
			requestTrackers: []string{"AITHER"},
			wantFailure:     api.OperationFailureMissingPrerequisite,
		},
		{
			name:            "foreign owner",
			owner:           "session-a",
			requestOwner:    "session-b",
			jobRelease:      release,
			jobRuntime:      41,
			jobTrackers:     []string{"AITHER"},
			requestTrackers: []string{"AITHER"},
			wantFailure:     api.OperationFailureMissingPrerequisite,
		},
		{
			name:            "stale release",
			owner:           "session-a",
			requestOwner:    "session-a",
			jobRelease:      api.ReleaseRef{SourcePath: release.SourcePath, Generation: 6},
			jobRuntime:      41,
			jobTrackers:     []string{"AITHER"},
			requestTrackers: []string{"AITHER"},
			wantFailure:     api.OperationFailureStaleGeneration,
		},
		{
			name:            "stale runtime",
			owner:           "session-a",
			requestOwner:    "session-a",
			jobRelease:      release,
			jobRuntime:      40,
			jobTrackers:     []string{"AITHER"},
			requestTrackers: []string{"AITHER"},
			wantFailure:     api.OperationFailureStaleGeneration,
		},
		{
			name:            "tracker mismatch",
			owner:           "session-a",
			requestOwner:    "session-a",
			jobRelease:      release,
			jobRuntime:      41,
			jobTrackers:     []string{"AITHER"},
			requestTrackers: []string{"AITHER", "BLU"},
			wantFailure:     api.OperationFailureMissingPrerequisite,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			capture := &dryRunCaptureCapability{}
			backend := &Backend{
				capabilities:      CoreCapabilities{DryRun: capture},
				runtimeGeneration: 41,
				hub:               newEventHub(),
				jobEngine:         sharedjobs.New(nil, sharedjobs.Config{}),
				jobOwners:         make(map[string]*sharedjobs.OwnerHandle),
			}
			t.Cleanup(backend.jobEngine.Close)
			jobID := startCompletedDryRunDupeJob(t, backend, test.owner, test.jobRelease, test.jobRuntime, test.jobTrackers)
			if test.requestJob != "" {
				jobID = test.requestJob
			}
			_, err := backend.FetchTrackerDryRun(
				context.Background(), test.requestOwner, jobID, release, test.requestTrackers, nil, nil, nil, true, "",
			)
			failure, ok := api.AsOperationFailure(err)
			if !ok || failure.Code != test.wantFailure {
				t.Fatalf("failure = %#v err=%v", failure, err)
			}
			if capture.plan.Input.Release.Generation != 0 {
				t.Fatalf("dry-run capability was invoked: %#v", capture.plan)
			}
		})
	}
}

func TestFetchTrackerDryRunRejectsRunningAndFailedDupeJobs(t *testing.T) {
	t.Parallel()

	release := api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 7}
	tests := []struct {
		name   string
		runner sharedjobs.DupeRunner
		wait   string
	}{
		{
name: "running",
 runner: blockingDryRunDupeRunner{started: make(chan struct{}), release: make(chan struct{})},
 wait: sharedjobs.StatusRunning,
},
		{
name: "failed",
 runner: failedDryRunDupeRunner{},
 wait: sharedjobs.StatusFailed,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newDryRunTestBackend(&dryRunCaptureCapability{})
			t.Cleanup(backend.jobEngine.Close)
			jobID := startDryRunDupeJob(t, backend, "session-a", release, 41, []string{"AITHER"}, test.runner)
			waitForDryRunDupeStatus(t, backend, "session-a", jobID, test.wait)
			_, err := backend.FetchTrackerDryRun(
				context.Background(), "session-a", jobID, release, []string{"AITHER"}, nil, nil, nil, true, "",
			)
			failure, ok := api.AsOperationFailure(err)
			if !ok || failure.Code != api.OperationFailureMissingPrerequisite {
				t.Fatalf("failure = %#v err=%v", failure, err)
			}
			if runner, ok := test.runner.(blockingDryRunDupeRunner); ok {
				close(runner.release)
			}
		})
	}
}

func TestFetchTrackerDryRunDetachesAcceptedEvidenceFromJobSnapshot(t *testing.T) {
	t.Parallel()

	capture := &dryRunCaptureCapability{}
	backend := newDryRunTestBackend(capture)
	t.Cleanup(backend.jobEngine.Close)
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 7}
	jobID := startCompletedDryRunDupeJob(t, backend, "session-a", release, 41, []string{"AITHER"})
	if _, err := backend.FetchTrackerDryRun(
		context.Background(), "session-a", jobID, release, []string{"AITHER"}, nil, nil, nil, true, "",
	); err != nil {
		t.Fatalf("fetch tracker dry run: %v", err)
	}
	capture.plan.Duplicate.Results[0].Notes[0] = "mutated"
	snapshot, err := backend.jobEngine.DupeSnapshot(backend.lookupJobOwner("session-a"), jobID)
	if err != nil {
		t.Fatalf("reload duplicate snapshot: %v", err)
	}
	if got := snapshot.Summary.Results[0].Notes[0]; got != "retained" {
		t.Fatalf("retained duplicate evidence mutated: %q", got)
	}
}

func newDryRunTestBackend(capability DryRunCapability) *Backend {
	return &Backend{
		capabilities:      CoreCapabilities{DryRun: capability},
		runtimeGeneration: 41,
		hub:               newEventHub(),
		jobEngine:         sharedjobs.New(nil, sharedjobs.Config{}),
		jobOwners:         make(map[string]*sharedjobs.OwnerHandle),
	}
}

func startCompletedDryRunDupeJob(
	t *testing.T,
	backend *Backend,
	ownerID string,
	release api.ReleaseRef,
	runtimeGeneration uint64,
	trackers []string,
) string {
	t.Helper()
	jobID := startDryRunDupeJob(t, backend, ownerID, release, runtimeGeneration, trackers, completedDryRunDupeRunner{})
	waitForDryRunDupeStatus(t, backend, ownerID, jobID, sharedjobs.StatusCompleted)
	return jobID
}

func startDryRunDupeJob(
	t *testing.T,
	backend *Backend,
	ownerID string,
	release api.ReleaseRef,
	runtimeGeneration uint64,
	trackers []string,
	runner sharedjobs.DupeRunner,
) string {
	t.Helper()
	owner, err := backend.ensureJobOwner(ownerID)
	if err != nil {
		t.Fatalf("ensure job owner: %v", err)
	}
	jobID, err := backend.jobEngine.StartDupe(context.Background(), owner, sharedjobs.DupeSpec{
		CorrelationID: "dry-run-prerequisite",
		Snapshot: sharedjobs.DuplicateExecutionSnapshot{
			PreparedGeneration: release.Generation,
			RuntimeGeneration:  runtimeGeneration,
			Input: api.DuplicateCheckInput{
				Release:  release,
				Trackers: append([]string(nil), trackers...),
			},
		},
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("start dupe job: %v", err)
	}
	return jobID
}

func waitForDryRunDupeStatus(t *testing.T, backend *Backend, ownerID string, jobID string, wantStatus string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, snapshotErr := backend.jobEngine.DupeSnapshot(backend.lookupJobOwner(ownerID), jobID)
		if snapshotErr != nil {
			t.Fatalf("load dupe snapshot: %v", snapshotErr)
		}
		if snapshot.Status == wantStatus {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for duplicate job status %q", wantStatus)
}

func TestFetchTrackerDryRunPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	backend := &Backend{
		capabilities:      CoreCapabilities{DryRun: &dryRunCaptureCapability{}},
		runtimeGeneration: 41,
		hub:               newEventHub(),
		jobEngine:         sharedjobs.New(nil, sharedjobs.Config{}),
		jobOwners:         make(map[string]*sharedjobs.OwnerHandle),
	}
	t.Cleanup(backend.jobEngine.Close)
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.1080p-GRP.mkv", Generation: 7}
	jobID := startCompletedDryRunDupeJob(t, backend, "session-a", release, 41, []string{"AITHER"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := backend.FetchTrackerDryRun(ctx, "session-a", jobID, release, []string{"AITHER"}, nil, nil, nil, true, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}
