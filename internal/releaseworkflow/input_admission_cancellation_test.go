// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type heldAdmissionEvents struct {
	*PersistentRepository
	hold     atomic.Bool
	queued   chan struct{}
	release  chan struct{}
	stopList atomic.Bool
	listing  chan struct{}
}

func (r *heldAdmissionEvents) AppendEvents(ctx context.Context, owner string, workflow api.WorkflowID, events []api.WorkflowEvent) ([]api.WorkflowEvent, error) {
	if r.hold.Swap(false) {
		close(r.queued)
		<-r.release
	}
	return r.PersistentRepository.AppendEvents(ctx, owner, workflow, events)
}

func (r *heldAdmissionEvents) ListActiveOperations(ctx context.Context) ([]api.ReleaseWorkflowOperationRecord, error) {
	if r.stopList.Swap(false) {
		close(r.listing)
		<-ctx.Done()
	}
	return r.PersistentRepository.ListActiveOperations(ctx)
}

type observedInputReservation struct {
	api.ActiveInputRepository
	workerReleased *atomic.Bool
	premature      atomic.Bool
	switchContext  chan context.Context
	once           sync.Once
}

func (r *observedInputReservation) CompareAndSwapActiveInput(ctx context.Context, prior, next api.ActiveInputRecord, now time.Time) error {
	if prior.State == api.ActiveInputSwitchPending && next.State == api.ActiveInputActive && !r.workerReleased.Load() {
		r.premature.Store(true)
	}
	err := r.ActiveInputRepository.CompareAndSwapActiveInput(ctx, prior, next, now)
	if err != nil {
		return fmt.Errorf("test input reservation: %w", err)
	}
	if next.State == api.ActiveInputSwitchPending {
		r.once.Do(func() {
			r.switchContext <- ctx
		})
	}
	return nil
}

func TestCanceledInputSwitchJoinsRunningAndQueuedWorkerBeforeRollback(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "running"
		if queued {
			name = "queued_before_dispatch"
		}
		t.Run(name, func(t *testing.T) {
			repo, err := db.Open(filepath.Join(t.TempDir(), "input.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			events := &heldAdmissionEvents{
				PersistentRepository: persistent,
				queued:               make(chan struct{}),
				release:              make(chan struct{}),
				listing:              make(chan struct{}),
			}
			workStarted, workFinish := make(chan struct{}), make(chan struct{})
			workCanceled := make(chan struct{})
			var released atomic.Bool
			var releaseWork, releaseEvents sync.Once
			inputs := &observedInputReservation{
				ActiveInputRepository: repo,
				workerReleased:        &released,
				switchContext:         make(chan context.Context, 1),
			}
			preparer := testPreparer()
			preparer.PrepareFunc = func(ctx context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
				close(workStarted)
				select {
				case <-ctx.Done():
					close(workCanceled)
				case <-workFinish:
					return api.PrepareResult{}, ctx.Err()
				}
				<-workFinish
				return api.PrepareResult{}, ctx.Err()
			}
			module, err := New(events, NewMemoryPrivateResourceStore(), preparer, WithActiveInputs(inputs,
				func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
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
				releaseEvents.Do(func() { close(events.release) })
				released.Store(true)
				releaseWork.Do(func() { close(workFinish) })
				_ = module.Shutdown(context.Background())
			})
			input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")}
			prior, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{Input: input, IdempotencyKey: "first"})
			if err != nil {
				t.Fatal(err)
			}
			state, err := persistent.Load(t.Context(), testOwnerID, prior.WorkflowID)
			if err != nil {
				t.Fatal(err)
			}
			events.hold.Store(queued)
			type acceptance struct {
				operation api.WorkflowOperationStatus
				err       error
			}
			started := make(chan acceptance, 1)
			startCtx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: prior.CoordinatorID, Fence: prior.Fence})
			go func() {
				operation, startErr := module.Start(startCtx, testOwnerID, PrepareReleaseCommand{
					WorkflowID:       prior.WorkflowID,
					ExpectedRevision: state.Workflow.Revision,
					Input:            input,
					IdempotencyKey:   "old-work",
				})
				started <- acceptance{operation, startErr}
			}()
			if queued {
				<-events.queued
			} else {
				<-workStarted
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			events.stopList.Store(true)
			opened := make(chan error, 1)
			go func() {
				_, openErr := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
					ExpectedRevision: prior.Revision,
					Input:            input,
					IdempotencyKey:   "switch",
				})
				opened <- openErr
			}()
			switchContext := <-inputs.switchContext
			<-events.listing
			cancel()
			<-switchContext.Done()
			if !queued {
				select {
				case <-workCanceled:
				case <-time.After(10 * time.Second):
					t.Fatal("canceled admission did not signal the prior worker")
				}
			}
			select {
			case err := <-opened:
				t.Fatalf("input switch returned before worker cleanup: %v", err)
			default:
			}
			pending, err := repo.LoadActiveInput(t.Context())
			if err != nil || pending.State != api.ActiveInputSwitchPending || pending.Fence != prior.Fence || !pending.LeaseExpiresAt.After(time.Now()) {
				t.Fatalf("live worker lost input authority: state=%s fence=%d err=%v", pending.State, pending.Fence, err)
			}
			released.Store(true)
			releaseEvents.Do(func() { close(events.release) })
			releaseWork.Do(func() { close(workFinish) })
			accepted := <-started
			if accepted.err != nil {
				t.Fatal(accepted.err)
			}
			if err := <-opened; err == nil {
				t.Fatal("canceled switch reported success")
			}
			if inputs.premature.Load() {
				t.Fatal("rollback ran before the held worker was released")
			}
			operation, err := module.Operation(t.Context(), testOwnerID, prior.WorkflowID, accepted.operation.ID)
			if err != nil || operation.Status == api.StageStatusQueued || operation.Status == api.StageStatusRunning {
				t.Fatalf("prior worker did not persist its terminal outcome: status=%s err=%v", operation.Status, err)
			}
			restored, err := repo.LoadActiveInput(t.Context())
			if err != nil || restored.State != api.ActiveInputActive || restored.InputID != prior.InputID || restored.Fence != prior.Fence {
				t.Fatalf("prior input was not restored: state=%s err=%v", restored.State, err)
			}
			if _, err := module.OpenInput(t.Context(), testOwnerID, OpenInputRequest{
				ExpectedRevision: restored.Revision,
				Input:            input,
				IdempotencyKey:   "retry",
			}); err != nil {
				t.Fatalf("explicit retry after worker cleanup: %v", err)
			}
		})
	}
}
