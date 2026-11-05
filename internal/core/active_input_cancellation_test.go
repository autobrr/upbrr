// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInputOpenCancellationJoinsVerificationAndFencesAcceptedWork(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	entered, cancelled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var finishOnce sync.Once
	t.Cleanup(func() { finishOnce.Do(func() { close(finish) }) })
	var verifications, preparations atomic.Int64
	workStarted, workFinish := make(chan struct{}), make(chan struct{})
	var workOnce sync.Once
	t.Cleanup(func() { workOnce.Do(func() { close(workFinish) }) })
	workflow, err := releaseworkflow.New(persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			preparations.Add(1)
			close(workStarted)
			select {
			case <-workFinish:
			case <-ctx.Done():
				return api.PrepareResult{}, ctx.Err()
			}
			return api.PrepareResult{Release: api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: input.SourcePath}}}, nil
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{}, nil
		},
	}, releaseworkflow.WithActiveInputs(repo, func(ctx context.Context, input api.PrepareInput) (api.InputRecord, error) {
		if verifications.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-finish
			return api.InputRecord{}, ctx.Err()
		}
		return api.InputRecord{
			CanonicalPath: input.SourcePath,
			SourceVersion: "synthetic",
			Manifest:      []byte(`{"identity":{"digest":"synthetic"}}`),
		}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		finishOnce.Do(func() { close(finish) })
		workOnce.Do(func() { close(workFinish) })
		_ = workflow.Shutdown(context.Background())
	})
	core := &Core{workflow: workflow, logger: api.NopLogger{}}
	request := api.OpenActiveInputRequest{Request: api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "owned-open",
		Goal:           api.WorkflowGoalPrepared,
		Intent:         api.WorkflowIntent{Preparation: &api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")}},
	}}
	opened := make(chan error, 1)
	go func() { _, err := core.OpenActiveInput(t.Context(), "owner", request); opened <- err }()
	<-entered
	for _, wrong := range []struct{ owner, key string }{{"another-owner", "owned-open"}, {"owner", "another-open"}} {
		if _, err := core.CancelActiveInputOpen(t.Context(), wrong.owner, api.CancelActiveInputOpenRequest{IdempotencyKey: wrong.key}); !errors.Is(err, releaseworkflow.ErrWorkflowNotFound) {
			t.Fatalf("unowned open cancellation: %v", err)
		}
	}
	type result struct {
		snapshot api.ActiveInputSnapshot
		err      error
	}
	stopped := make(chan result, 1)
	go func() {
		snapshot, err := core.CancelActiveInputOpen(t.Context(), "owner", api.CancelActiveInputOpenRequest{IdempotencyKey: "owned-open"})
		stopped <- result{snapshot, err}
	}()
	<-cancelled
	select {
	case <-stopped:
		t.Fatal("cancellation claimed completion before verifier cleanup")
	default:
	}
	if preparations.Load() != 0 {
		t.Fatal("preparation started after verification cancellation")
	}
	finishOnce.Do(func() { close(finish) })
	if err := <-opened; !errors.Is(err, context.Canceled) {
		t.Fatalf("open cancellation: %v", err)
	}
	stop := <-stopped
	if stop.err != nil || stop.snapshot.State != api.ActiveInputEmpty || stop.snapshot.Revision != 2 {
		t.Fatalf("cancelled admission did not finish rollback: state=%s revision=%d err=%v", stop.snapshot.State, stop.snapshot.Revision, stop.err)
	}
	request.ExpectedRevision = stop.snapshot.Revision
	request.Request.IdempotencyKey = "explicit-retry"
	retry, err := core.OpenActiveInput(t.Context(), "owner", request)
	if err != nil {
		t.Fatal(err)
	}
	<-workStarted
	if _, err := core.CancelActiveInputOpen(t.Context(), "owner", api.CancelActiveInputOpenRequest{IdempotencyKey: "owned-open"}); !errors.Is(err, releaseworkflow.ErrWorkflowNotFound) {
		t.Fatalf("late open cancellation affected accepted work: %v", err)
	}
	workOnce.Do(func() { close(workFinish) })
	if retry.Current == nil || retry.Current.Operation == nil {
		t.Fatal("retry did not accept an operation")
	}
	operation := *retry.Current.Operation
	for operation.Status == api.StageStatusQueued || operation.Status == api.StageStatusRunning {
		operation, err = workflow.Operation(t.Context(), "owner", retry.Current.Workflow.ID, operation.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if operation.Status != api.StageStatusCompleted {
		t.Fatalf("explicit retry outcome=%s", operation.Status)
	}
}
