// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDupeCanonicalResultsDiscoveryAndRepeatedTerminalProgress(t *testing.T) {
	t.Parallel()
	runner := dupeRunnerFunc(func(ctx context.Context, _ api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
			Tracker: "a",
			Status:  "running",
			Total:   3,
		})
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
			Tracker: "a",
			Status:  "completed",
			Total:   2,
			Result:  api.DupeCheckResult{Tracker: "a"},
		})
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
			Tracker: "a",
			Status:  "completed",
			Total:   1,
			Result:  api.DupeCheckResult{Tracker: "a"},
		})
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{
			Tracker: " discovered ",
			Status:  "skipped",
			Result:  api.DupeCheckResult{Tracker: "discovered", Skipped: true},
		})
		return api.DupeCheckSummary{Results: []api.DupeCheckResult{
			{
				Tracker:  "A",
				HasDupes: true,
				Filtered: []api.DupeEntry{{}},
			},
			{Tracker: "B", Status: "completed"},
		}}, nil
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	id, err := engine.StartDupe(context.Background(), mustRegisterOwner(t, engine, "owner"), dupeSpec(runner, "a"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	snapshot := waitDupe(t, engine, id)
	if snapshot.Status != StatusCompleted || snapshot.CompletedCount != 3 || snapshot.TotalCount != 3 {
		t.Fatalf("counts/status = %#v", snapshot)
	}
	if len(snapshot.Trackers) != 3 || snapshot.Trackers[0].Tracker != "A" || snapshot.Trackers[1].Tracker != "DISCOVERED" || snapshot.Trackers[2].Tracker != "B" {
		t.Fatalf("tracker order = %#v", snapshot.Trackers)
	}
}

func TestDupeCancellationMarksEveryActiveTracker(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	runner := dupeRunnerFunc(func(ctx context.Context, _ api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		api.EmitDupeProgress(ctx, api.DupeProgressUpdate{Tracker: "A", Status: "completed"})
		close(started)
		<-ctx.Done()
		return api.DupeCheckSummary{}, ctx.Err()
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	id, err := engine.StartDupe(context.Background(), owner, dupeSpec(runner, "A", "B"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	<-started
	if err := engine.CancelDupe(owner, id); err != nil {
		t.Fatalf("CancelDupe: %v", err)
	}
	snapshot := waitDupe(t, engine, id)
	if snapshot.Status != StatusCanceled || snapshot.Trackers[0].Status != StatusCompleted || snapshot.Trackers[1].Status != StatusCanceled {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, state := range snapshot.Trackers {
		if state.Status == StatusQueued || state.Status == StatusRunning || state.FinishedAt == "" {
			t.Fatalf("nonterminal state = %#v", state)
		}
	}
}

func TestDupeCoreErrorIsSanitizedAndLeavesNoActiveStates(t *testing.T) {
	t.Parallel()
	runner := dupeRunnerFunc(func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		return api.DupeCheckSummary{}, errors.New("api_key=secret-value")
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	id, err := engine.StartDupe(context.Background(), mustRegisterOwner(t, engine, "owner"), dupeSpec(runner, "A", "B"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	snapshot := waitDupe(t, engine, id)
	if snapshot.Status != StatusFailed || snapshot.Failure == nil || strings.Contains(snapshot.Failure.Message, "secret-value") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, state := range snapshot.Trackers {
		if state.Status != StatusFailed || strings.Contains(state.Message, "secret-value") {
			t.Fatalf("state = %#v", state)
		}
	}
}

func TestDupeSnapshotPreservesTypedOperationFailure(t *testing.T) {
	t.Parallel()
	want := api.OperationFailure{
		Code:      api.OperationFailureConfirmationRequired,
		Operation: api.OperationKindDuplicateCheck,
		Message:   "Blu-ray playlist changes require confirmation before rescanning.",
		Recovery:  api.OperationRecoveryConfirm,
	}
	runner := dupeRunnerFunc(func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		return api.DupeCheckSummary{}, api.NewOperationError(want, errors.New("private cause"))
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	id, err := engine.StartDupe(context.Background(), mustRegisterOwner(t, engine, "owner"), dupeSpec(runner, "A"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	snapshot := waitDupe(t, engine, id)
	if snapshot.Failure == nil || *snapshot.Failure != want {
		t.Fatalf("failure = %#v, want %#v", snapshot.Failure, want)
	}
}

func TestDupeResultErrorIsSanitizedBeforeSnapshotDelivery(t *testing.T) {
	t.Parallel()
	runner := dupeRunnerFunc(func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		return api.DupeCheckSummary{Results: []api.DupeCheckResult{{
			Tracker: "A",
			Status:  StatusFailed,
			Error:   "token=secret-value",
		}}}, nil
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	id, err := engine.StartDupe(context.Background(), mustRegisterOwner(t, engine, "owner"), dupeSpec(runner, "A"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	snapshot := waitDupe(t, engine, id)
	if strings.Contains(snapshot.Trackers[0].Result.Error, "secret-value") || strings.Contains(snapshot.Summary.Results[0].Error, "secret-value") {
		t.Fatalf("secret crossed result snapshot: %#v", snapshot)
	}
}

func TestDupeResourceFailureDoesNotChangeOutcome(t *testing.T) {
	t.Parallel()
	closer := &closeRecorder{err: errors.New("token=secret-value")}
	runner := dupeRunnerFunc(func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		return api.DupeCheckSummary{}, nil
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	spec := dupeSpec(runner, "A")
	spec.Resources.Core = closer
	id, err := engine.StartDupe(context.Background(), mustRegisterOwner(t, engine, "owner"), spec)
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	snapshot := waitDupe(t, engine, id)
	if snapshot.Status != StatusCompleted || snapshot.Failure != nil || closer.count.Load() != 1 {
		t.Fatalf("snapshot=%#v closes=%d", snapshot, closer.count.Load())
	}
}
