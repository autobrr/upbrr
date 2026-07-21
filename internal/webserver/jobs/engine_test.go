// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type recordingSink struct {
	mu       sync.Mutex
	uploads  []TrackerUploadSnapshot
	dupes    []DupeCheckSnapshot
	warnings []string
}

func (s *recordingSink) WarnJob(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = append(s.warnings, message)
}

func (s *recordingSink) EmitUpload(_ string, snapshot TrackerUploadSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, snapshot)
}

func (s *recordingSink) EmitDupe(_ string, snapshot DupeCheckSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dupes = append(s.dupes, snapshot)
}

func mustRegisterOwner(t *testing.T, engine *Engine, ownerID string) *OwnerHandle {
	t.Helper()
	owner, err := engine.RegisterOwner(ownerID)
	if err != nil {
		t.Fatalf("RegisterOwner(%q): %v", ownerID, err)
	}
	return owner
}

type uploadRunnerFunc func(context.Context, api.UploadExecutionPlan) (api.Result, error)

func (f uploadRunnerFunc) RunUpload(ctx context.Context, request api.UploadExecutionPlan) (api.Result, error) {
	return f(ctx, request)
}

type dupeRunnerFunc func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error)

func (f dupeRunnerFunc) CheckDupes(ctx context.Context, request api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
	return f(ctx, request)
}

type closeRecorder struct {
	count      atomic.Int32
	err        error
	panicValue any
	started    chan struct{}
	release    <-chan struct{}
}

func (c *closeRecorder) Close() error {
	c.count.Add(1)
	if c.started != nil {
		close(c.started)
	}
	if c.release != nil {
		<-c.release
	}
	if c.panicValue != nil {
		panic(c.panicValue)
	}
	return c.err
}

func TestTerminalStateWaitsForResourceRelease(t *testing.T) {
	t.Parallel()
	cleanupStarted := make(chan struct{})
	cleanupRelease := make(chan struct{})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	id, err := engine.StartUpload(context.Background(), owner, UploadSpec{
		CorrelationID: "cleanup-correlation",
		Snapshot: testUploadSnapshot("Example Release 2026"),
		Runner: uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) {
			return api.Result{UploadedCount: 1}, nil
		}),
		Resources: Resources{Core: &closeRecorder{started: cleanupStarted, release: cleanupRelease}},
	})
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	<-cleanupStarted
	snapshot, err := engine.UploadSnapshot(owner, id)
	if err != nil {
		t.Fatalf("UploadSnapshot: %v", err)
	}
	if isJobTerminal(snapshot.Status) || snapshot.FinishedAt != "" {
		t.Fatalf("terminal state published before cleanup: %#v", snapshot)
	}
	close(cleanupRelease)
	if snapshot = waitUpload(t, engine, id); snapshot.Status != StatusCompleted {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func uploadSpec(runner UploadRunner, trackers ...string) UploadSpec {
	sourcePath := `C:\Example\Example.Release.2026.1080p-GRP`
	snapshot := testUploadSnapshot(sourcePath)
	snapshot.Input.Trackers = append([]string(nil), trackers...)
	return UploadSpec{
CorrelationID: "upload-correlation",
 Snapshot: snapshot,
 Runner: runner,
}
}

func dupeSpec(runner DupeRunner, trackers ...string) DupeSpec {
	sourcePath := `C:\Example\Example.Release.2026.1080p-GRP`
	snapshot := testDupeSnapshot(sourcePath)
	snapshot.Input.Trackers = append([]string(nil), trackers...)
	return DupeSpec{
CorrelationID: "dupe-correlation",
 Snapshot: snapshot,
 Runner: runner,
}
}

func testUploadSnapshot(sourcePath string) UploadExecutionSnapshot {
	return UploadExecutionSnapshot{
		PreparedGeneration: 1,
		RuntimeGeneration:  1,
		Input: api.UploadReviewInput{
			Release:  api.ReleaseRef{SourcePath: sourcePath, Generation: 1},
			Trackers: []string{"A"},
		},
		ReviewToken: "test-review-token",
	}
}

func testDupeSnapshot(sourcePath string) DuplicateExecutionSnapshot {
	return DuplicateExecutionSnapshot{
		PreparedGeneration: 1,
		RuntimeGeneration:  1,
		Input: api.DuplicateCheckInput{
			Release:  api.ReleaseRef{SourcePath: sourcePath, Generation: 1},
			Trackers: []string{"A"},
		},
	}
}

func waitUpload(t *testing.T, engine *Engine, id string) TrackerUploadSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := engine.UploadSnapshot(mustRegisterOwner(t, engine, "owner"), id)
		if err == nil && isJobTerminal(snapshot.Status) {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("upload did not become terminal")
	return TrackerUploadSnapshot{}
}

func waitDupe(t *testing.T, engine *Engine, id string) DupeCheckSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := engine.DupeSnapshot(mustRegisterOwner(t, engine, "owner"), id)
		if err == nil && isJobTerminal(snapshot.Status) {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("dupe check did not become terminal")
	return DupeCheckSnapshot{}
}

func TestEngineValidatesInputsAndIsolatesOwners(t *testing.T) {
	t.Parallel()
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	runner := uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) { return api.Result{}, nil })

	if _, err := engine.RegisterOwner(""); err == nil {
		t.Fatal("expected owner validation error")
	}
	owner := mustRegisterOwner(t, engine, "owner")
	if _, err := engine.StartUpload(context.Background(), owner, uploadSpec(runner)); err == nil {
		t.Fatal("expected tracker validation error")
	}
	id, err := engine.StartUpload(context.Background(), owner, uploadSpec(runner, " A ", "a", "B"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	other := mustRegisterOwner(t, engine, "other")
	if _, err := engine.UploadSnapshot(other, id); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("wrong-owner snapshot error = %v", err)
	}
	if err := engine.CancelUpload(other, id); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("wrong-owner cancel error = %v", err)
	}
	if _, err := engine.UploadRetry(other, id); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("wrong-owner retry error = %v", err)
	}
	snapshot := waitUpload(t, engine, id)
	if len(snapshot.Trackers) != 2 || snapshot.Trackers[0].Tracker != "A" || snapshot.Trackers[1].Tracker != "B" {
		t.Fatalf("normalized trackers = %#v", snapshot.Trackers)
	}
}

func TestEngineCloseWaitsCancelsAndRejectsStarts(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	runner := uploadRunnerFunc(func(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
		close(started)
		<-ctx.Done()
		return api.Result{}, ctx.Err()
	})
	core, logger := &closeRecorder{}, &closeRecorder{}
	engine := New(nil, Config{})
	owner := mustRegisterOwner(t, engine, "owner")
	_, err := engine.StartUpload(context.Background(), owner, UploadSpec{
		CorrelationID: "rejected-correlation",
		Snapshot:  testUploadSnapshot("Example Release 2026"),
		Runner:    runner,
		Resources: Resources{Core: core, Logger: logger},
	})
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	<-started
	engine.Close()
	if _, err := engine.UploadSnapshot(owner, "discarded"); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("snapshot after close error = %v", err)
	}
	if core.count.Load() != 1 || logger.count.Load() != 1 {
		t.Fatalf("close counts core=%d logger=%d", core.count.Load(), logger.count.Load())
	}
	if _, err := engine.RegisterOwner("new-owner"); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("register after close error = %v", err)
	}
	engine.Close()
}

func TestOwnerRemovalDrainsAndStaleHandleNeverRevives(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	core := &closeRecorder{}
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "session")
	runner := uploadRunnerFunc(func(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
		close(started)
		<-ctx.Done()
		<-release
		return api.Result{}, ctx.Err()
	})
	jobID, err := engine.StartUpload(context.Background(), owner, UploadSpec{
		CorrelationID: "owner-removal-correlation",
		Snapshot:  testUploadSnapshot("Example Release 2026"),
		Runner:    runner,
		Resources: Resources{Core: core},
	})
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	<-started
	done := make(chan error, 1)
	go func() { done <- engine.RemoveOwner(owner) }()

	deadline := time.Now().Add(3 * time.Second)
	for !owner.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !owner.closed.Load() {
		t.Fatal("owner did not enter draining state")
	}
	if _, err := engine.RegisterOwner("session"); !errors.Is(err, ErrOwnerDraining) {
		t.Fatalf("register during drain error = %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("RemoveOwner returned before worker release: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RemoveOwner: %v", err)
	}
	if core.count.Load() != 1 {
		t.Fatalf("core close count = %d", core.count.Load())
	}
	if _, err := engine.UploadSnapshot(owner, jobID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("stale snapshot error = %v", err)
	}
	if _, err := engine.StartUpload(context.Background(), owner, uploadSpec(runner, "A")); !errors.Is(err, ErrOwnerClosed) {
		t.Fatalf("stale start error = %v", err)
	}
	reused := mustRegisterOwner(t, engine, "session")
	if reused == owner || reused.generation == owner.generation {
		t.Fatal("same textual owner ID revived the stale generation")
	}
}

func TestAcceptedJobSurvivesCallerCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	runner := uploadRunnerFunc(func(ctx context.Context, _ api.UploadExecutionPlan) (api.Result, error) {
		close(started)
		select {
		case <-ctx.Done():
			return api.Result{}, errors.New("Job context canceled by caller")
		case <-release:
			return api.Result{UploadedCount: 1}, nil
		}
	})
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	requestCtx, cancel := context.WithCancel(context.Background())
	id, err := engine.StartUpload(requestCtx, owner, uploadSpec(runner, "A"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	<-started
	cancel()
	close(release)
	if snapshot := waitUpload(t, engine, id); snapshot.Status != StatusCompleted {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestCleanupFailureWarnsWithoutRewritingOutcome(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	engine := New(sink, Config{})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	id, err := engine.StartUpload(context.Background(), owner, UploadSpec{
		CorrelationID: "panic-correlation",
		Snapshot: testUploadSnapshot("Example Release 2026"),
		Runner: uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) {
			return api.Result{UploadedCount: 1}, nil
		}),
		Resources: Resources{Core: &closeRecorder{err: errors.New("token=secret-value")}},
	})
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if snapshot := waitUpload(t, engine, id); snapshot.Status != StatusCompleted {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.warnings) != 1 || strings.Contains(sink.warnings[0], "secret-value") {
		t.Fatalf("warnings = %#v", sink.warnings)
	}
}

func TestOwnerChurnLeavesNoIndexEntries(t *testing.T) {
	t.Parallel()
	engine := New(nil, Config{})
	t.Cleanup(engine.Close)
	for idx := range 100 {
		owner := mustRegisterOwner(t, engine, fmt.Sprintf("owner-%d", idx))
		if err := engine.RemoveOwner(owner); err != nil {
			t.Fatalf("RemoveOwner(%d): %v", idx, err)
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.owners) != 0 || len(engine.records) != 0 {
		t.Fatalf("owner index=%d records=%d", len(engine.owners), len(engine.records))
	}
}

func TestListIsOwnerScopedOrderedAndCarriesCorrelationRetryLinkage(t *testing.T) {
	t.Parallel()
	var sequence atomic.Int64
	engine := newEngine(nil, Config{}, engineDeps{
		now:          func() time.Time { return time.Unix(sequence.Add(1), 0) },
		newID:        func() string { return fmt.Sprintf("job-%d", sequence.Add(1)) },
		retentionTTL: time.Hour,
		retentionMax: 200,
	})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	other := mustRegisterOwner(t, engine, "owner-b")

	failed := uploadSpec(uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) {
		return api.Result{}, trackerLocalUploadTestError{message: "failed"}
	}), "A")
	failed.CorrelationID = "start-correlation"
	originalID, err := engine.StartUpload(context.Background(), owner, failed)
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	_ = waitUpload(t, engine, originalID)

	retry, err := engine.UploadRetry(owner, originalID)
	if err != nil {
		t.Fatalf("UploadRetry: %v", err)
	}
	retryID, err := engine.StartUpload(context.Background(), owner, retry.Spec(
		"retry-correlation",
		uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) {
			return api.Result{UploadedCount: 1}, nil
		}),
		Resources{},
	))
	if err != nil {
		t.Fatalf("StartUpload retry: %v", err)
	}
	_ = waitUpload(t, engine, retryID)

	listed, err := engine.List(owner)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 || listed[0].JobID != originalID || listed[1].JobID != retryID {
		t.Fatalf("acceptance order = %#v", listed)
	}
	if listed[0].CorrelationID != "start-correlation" || listed[1].CorrelationID != "retry-correlation" || listed[1].RetryOf != originalID {
		t.Fatalf("correlation/retry linkage = %#v", listed)
	}
	foreign, err := engine.List(other)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign owner listing = %#v, %v", foreign, err)
	}

	listed[0].Upload.Trackers[0].Tracker = "mutated"
	listedAgain, err := engine.List(owner)
	if err != nil {
		t.Fatalf("List again: %v", err)
	}
	if listedAgain[0].Upload.Trackers[0].Tracker != "A" {
		t.Fatal("owner listing exposed mutable retained state")
	}
}
