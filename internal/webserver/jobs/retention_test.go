// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type retentionTimer struct {
	stopped  atomic.Bool
	callback func()
}

func (owned *retentionTimer) Stop() bool { return !owned.stopped.Swap(true) }

func (owned *retentionTimer) fire() {
	if !owned.stopped.Load() && owned.callback != nil {
		owned.callback()
	}
}

func TestRetentionPrunesOldestTerminalJobsAcrossKindsPerOwner(t *testing.T) {
	t.Parallel()
	var sequence atomic.Int64
	engine := newEngine(nil, Config{}, engineDeps{
		now:          func() time.Time { return time.Unix(sequence.Add(1), 0) },
		newID:        func() string { return fmt.Sprintf("job-%d", sequence.Add(1)) },
		retentionTTL: -1,
		retentionMax: 2,
	})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	uploadRunner := uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) { return api.Result{}, nil })
	dupeRunner := dupeRunnerFunc(func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		return api.DupeCheckSummary{}, nil
	})

	first, err := engine.StartUpload(context.Background(), owner, uploadSpec(uploadRunner, "A"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	_ = waitUpload(t, engine, first)
	second, err := engine.StartDupe(context.Background(), owner, dupeSpec(dupeRunner, "A"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	_ = waitDupe(t, engine, second)
	third, err := engine.StartUpload(context.Background(), owner, uploadSpec(uploadRunner, "B"))
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	_ = waitUpload(t, engine, third)

	if _, err := engine.UploadSnapshot(owner, first); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("oldest mixed-kind lookup error = %v", err)
	}
	if _, err := engine.DupeSnapshot(owner, second); err != nil {
		t.Fatalf("retained dupe %s: %v", second, err)
	}
	if _, err := engine.UploadSnapshot(owner, third); err != nil {
		t.Fatalf("retained upload %s: %v", third, err)
	}

	other := mustRegisterOwner(t, engine, "other")
	otherID, err := engine.StartUpload(context.Background(), other, uploadSpec(uploadRunner, "C"))
	if err != nil {
		t.Fatalf("StartUpload other: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, snapshotErr := engine.UploadSnapshot(other, otherID)
		if snapshotErr == nil && isJobTerminal(snapshot.Status) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := engine.UploadSnapshot(other, otherID); err != nil {
		t.Fatalf("other owner retention affected: %v", err)
	}
}

func TestRetentionTTLDeletesTerminalJob(t *testing.T) {
	t.Parallel()
	var timerMu sync.Mutex
	var created *retentionTimer
	engine := newEngine(nil, Config{}, engineDeps{
		retentionTTL: time.Hour,
		retentionMax: 200,
		newTimer: func(_ time.Duration, callback func()) timer {
			timer := &retentionTimer{callback: callback}
			timerMu.Lock()
			created = timer
			timerMu.Unlock()
			return timer
		},
	})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	runner := dupeRunnerFunc(func(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error) {
		return api.DupeCheckSummary{}, nil
	})
	id, err := engine.StartDupe(context.Background(), owner, dupeSpec(runner, "A"))
	if err != nil {
		t.Fatalf("StartDupe: %v", err)
	}
	_ = waitDupe(t, engine, id)
	timerMu.Lock()
	owned := created
	timerMu.Unlock()
	if owned == nil {
		t.Fatal("retention timer was not created")
	}
	owned.fire()
	if _, err := engine.DupeSnapshot(owner, id); !errors.Is(err, ErrDupeNotFound) {
		t.Fatalf("TTL lookup error = %v", err)
	}
}

func TestCapPruningStopsEvictedTTLTimer(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	created := make([]*retentionTimer, 0, 2)
	engine := newEngine(nil, Config{}, engineDeps{
		retentionTTL: time.Hour,
		retentionMax: 1,
		newTimer: func(_ time.Duration, callback func()) timer {
			owned := &retentionTimer{callback: callback}
			mu.Lock()
			created = append(created, owned)
			mu.Unlock()
			return owned
		},
	})
	t.Cleanup(engine.Close)
	owner := mustRegisterOwner(t, engine, "owner")
	runner := uploadRunnerFunc(func(context.Context, api.UploadExecutionPlan) (api.Result, error) { return api.Result{}, nil })
	for range 2 {
		id, err := engine.StartUpload(context.Background(), owner, uploadSpec(runner, "A"))
		if err != nil {
			t.Fatalf("StartUpload: %v", err)
		}
		_ = waitUpload(t, engine, id)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(created) != 2 || !created[0].stopped.Load() || created[1].stopped.Load() {
		t.Fatalf("timer state created=%d firstStopped=%v secondStopped=%v", len(created), created[0].stopped.Load(), created[1].stopped.Load())
	}
}
