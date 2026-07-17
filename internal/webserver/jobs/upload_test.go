// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type trackerLocalUploadTestError struct{ message string }

func (e trackerLocalUploadTestError) Error() string { return e.message }

func (trackerLocalUploadTestError) TrackerLocalUploadFailures() []string { return []string{"A"} }

func TestUploadPartialFailureContinuesAndBuildsClonedRetry(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var requests []api.UploadExecutionPlan
	runner := uploadRunnerFunc(func(_ context.Context, request api.UploadExecutionPlan) (api.Result, error) {
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		return api.Result{UploadedCount: 2}, trackerLocalUploadTestError{message: "token=secret-value failed"}
	})
	answers := map[string]map[string]string{"A": {"edition": "Example"}}
	ignore := []string{"C"}
	authorizations := []api.RuleAuthorization{{Tracker: "C", Rules: []string{"source_rule"}}}
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	spec := uploadSpec(runner, "A", "B")
	spec.Snapshot.Input.QuestionnaireAnswers = answers
	spec.Snapshot.Input.IgnoreDupesFor = ignore
	spec.Snapshot.Input.RuleAuthorizations = authorizations
	owner := mustRegisterOwner(t, engine, "owner")
	id, err := engine.StartUpload(context.Background(), owner, spec)
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	answers["A"]["edition"] = "mutated"
	ignore[0] = "mutated"
	authorizations[0].Rules[0] = "mutated"
	snapshot := waitUpload(t, engine, id)
	if snapshot.Status != StatusCompletedWithErrors || snapshot.UploadedCount != 2 {
		t.Fatalf("terminal snapshot = %#v", snapshot)
	}
	if snapshot.Failure == nil || strings.Contains(snapshot.Failure.Message, "secret-value") {
		t.Fatalf("unsafe or missing snapshot failure: %#v", snapshot.Failure)
	}
	if snapshot.Trackers[1].UploadedCount != 0 || snapshot.Trackers[1].Status != "success" {
		t.Fatalf("second tracker = %#v", snapshot.Trackers[1])
	}
	retry, err := engine.UploadRetry(owner, id)
	if err != nil {
		t.Fatalf("UploadRetry: %v", err)
	}
	if len(retry.Snapshot.Input.Trackers) != 1 || retry.Snapshot.Input.Trackers[0] != "A" || retry.Snapshot.Input.QuestionnaireAnswers["A"]["edition"] != "Example" || retry.Snapshot.Input.IgnoreDupesFor[0] != "C" || retry.Snapshot.Input.RuleAuthorizations[0].Rules[0] != "source_rule" {
		t.Fatalf("retry = %#v", retry)
	}
	retry.Snapshot.Input.Trackers[0] = "mutated"
	retry2, _ := engine.UploadRetry(owner, id)
	if retry2.Snapshot.Input.Trackers[0] != "A" {
		t.Fatal("retry exposes mutable retained trackers")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 ||
		!slices.Equal(requests[0].Input.Trackers, []string{"A", "B"}) ||
		requests[0].Input.QuestionnaireAnswers["A"]["edition"] != "Example" ||
		requests[0].Input.IgnoreDupesFor[0] != "C" ||
		requests[0].Input.RuleAuthorizations[0].Rules[0] != "source_rule" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestUploadCancellationPreservesPartialCountAndTerminalStates(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	runner := uploadRunnerFunc(func(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
		close(started)
		<-ctx.Done()
		return api.Result{UploadedCount: 1}, ctx.Err()
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	id, err := engine.StartUpload(context.Background(), owner, uploadSpec(runner, "A", "B"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	<-started
	if err := engine.CancelUpload(owner, id); err != nil {
		t.Fatalf("CancelUpload: %v", err)
	}
	snapshot := waitUpload(t, engine, id)
	if snapshot.Status != StatusCanceled || snapshot.UploadedCount != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, state := range snapshot.Trackers {
		if state.Status == StatusQueued || state.Status == StatusRunning {
			t.Fatalf("active terminal tracker = %#v", state)
		}
	}
}

func TestUploadProgressThrottleAndTrackerTargeting(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	runner := uploadRunnerFunc(func(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
		api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
			Tracker:     "A",
			Task:        "hash",
			Status:      "running",
			Percent:     10,
			HashRateMiB: 1,
		})
		api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
			Tracker:     "A",
			Task:        "hash",
			Status:      "running",
			Percent:     10,
			HashRateMiB: 1.1,
		})
		api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
			Tracker:     "A",
			Task:        "hash",
			Status:      "running",
			Percent:     11,
			HashRateMiB: 1.1,
		})
		return api.Result{}, nil
	})
	engine := New(sink, Config{UploadProgress: UploadProgressPolicy{MinInterval: 60_000_000_000, HashRateDeltaMiB: 1}})
	t.Cleanup(engine.Close)
	id, err := engine.StartUpload(context.Background(), mustRegisterOwner(t, engine, "owner"), uploadSpec(runner, "A"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	snapshot := waitUpload(t, engine, id)
	if snapshot.Trackers[0].Percent != 11 || snapshot.CurrentPercent != 11 {
		t.Fatalf("progress state = %#v", snapshot)
	}
	if snapshot.FailedTrackers == nil {
		t.Fatal("successful upload snapshot exposed null failedTrackers")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	// queued, running, first progress, percent change, terminal.
	if len(sink.uploads) != 5 {
		t.Fatalf("emission count = %d", len(sink.uploads))
	}
}

func TestUploadProgressMessageIsSanitizedBeforeSnapshotDelivery(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	runner := uploadRunnerFunc(func(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
		api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
			Tracker: "A",
			Task:    "hash",
			Status:  "running",
			Message: "  token=secret-value failed  ",
		})
		return api.Result{}, nil
	})
	engine := New(sink, Config{})
	t.Cleanup(engine.Close)
	id, err := engine.StartUpload(context.Background(), mustRegisterOwner(t, engine, "owner"), uploadSpec(runner, "A"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	snapshot := waitUpload(t, engine, id)
	if strings.Contains(snapshot.CurrentMessage, "secret-value") {
		t.Fatalf("secret crossed current progress field: %q", snapshot.CurrentMessage)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, emitted := range sink.uploads {
		if strings.Contains(emitted.CurrentMessage, "secret-value") {
			t.Fatalf("secret crossed emitted current field: %q", emitted.CurrentMessage)
		}
		for _, tracker := range emitted.Trackers {
			if strings.Contains(tracker.Message, "secret-value") {
				t.Fatalf("secret crossed emitted tracker field: %q", tracker.Message)
			}
		}
	}
}

func TestUploadPanicFailsButCloseFailurePreservesOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		runner UploadRunner
		closer *closeRecorder
		status string
	}{
		{
			name: "worker panic",
			runner: uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) {
				panic("token=secret-value")
			}),
			closer: &closeRecorder{},
			status: StatusFailed,
		},
		{
			name: "close panic",
			runner: uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) {
				return api.Result{UploadedCount: 1}, nil
			}),
			closer: &closeRecorder{panicValue: "password=secret-value"},
			status: StatusCompleted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := New(nil, Config{})
			t.Cleanup(engine.Close)
			spec := uploadSpec(test.runner, "A")
			spec.Resources.Core = test.closer
			id, err := engine.StartUpload(context.Background(), mustRegisterOwner(t, engine, "owner"), spec)
			if err != nil {
				t.Fatalf("StartUpload: %v", err)
			}
			snapshot := waitUpload(t, engine, id)
			if snapshot.Status != test.status || (snapshot.Failure != nil && strings.Contains(snapshot.Failure.Message, "secret-value")) {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			if test.closer.count.Load() != 1 {
				t.Fatalf("close count = %d", test.closer.count.Load())
			}
		})
	}
}
