// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCoordinatorShutdownRetainsHeartbeatUntilJoined(t *testing.T) {
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := &Coordinator{activeCancel: func() {}, activeDone: done}
	workerCtx, cancelWorker := context.WithCancel(t.Context())
	workerDone := make(chan struct{})
	c.operationWorkers = map[api.WorkflowOperationID]operationWorker{"worker": {cancel: cancelWorker, done: workerDone}}
	for range 2 {
		if err := c.Shutdown(ctx); !errors.Is(err, context.Canceled) || c.activeDone != done {
			t.Fatal("shutdown lost unfinished heartbeat")
		}
	}
	if workerCtx.Err() == nil {
		t.Fatal("blocked heartbeat prevented worker cancellation")
	}
	close(workerDone)
	close(done)
	if err := c.Shutdown(t.Context()); err != nil || c.activeDone != nil {
		t.Fatalf("joined shutdown = %v", err)
	}
}

type heldHeartbeatRepository struct {
	api.ActiveInputRepository
	renewing, release chan struct{}
}

func (r *heldHeartbeatRepository) RenewActiveInput(ctx context.Context, _ string, _ uint64, _, _ time.Time) error {
	close(r.renewing)
	<-r.release // Exercise a dependency that temporarily ignores cancellation.
	return fmt.Errorf("held renewal: %w", ctx.Err())
}

func TestHeartbeatReplacementCannotLoseBlockedPredecessor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := newCoordinator("heartbeat-test")
		if err != nil {
			t.Fatal(err)
		}
		repository := &heldHeartbeatRepository{renewing: make(chan struct{}), release: make(chan struct{})}
		m := &Module{
			Coordinator:  c,
			clock:        systemClock{},
			activeInputs: repository,
		}
		m.activeMu.Lock()
		err = m.startActiveInputHeartbeat(t.Context(), 1)
		m.activeMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		<-repository.renewing
		predecessor := m.activeDone
		for _, flow := range []string{"queue advancement", "correction replacement"} {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			m.activeMu.Lock()
			err = m.startActiveInputHeartbeat(ctx, 2)
			m.activeMu.Unlock()
			cancel()
			if err == nil || m.activeDone != predecessor {
				t.Fatalf("%s admitted untracked replacement", flow)
			}
			ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			if err = m.Coordinator.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) || m.activeDone != predecessor {
				t.Fatal("retry prematurely confirmed termination")
			}
			cancel()
		}
		close(repository.release)
		if err = m.Coordinator.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if m.activeDone != nil {
			t.Fatal("joined predecessor still retained")
		}
	})
}

type heldOperationLeaseRepository struct {
	*MemoryRepository
	entered, release, finished chan struct{}
}

func (r *heldOperationLeaseRepository) RenewWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	close(r.entered)
	defer close(r.finished)
	<-r.release
	return r.MemoryRepository.RenewWork(ctx, record)
}
func TestCoordinatorShutdownJoinsActualOperationLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := &heldOperationLeaseRepository{
			MemoryRepository: NewMemoryRepository(),
			entered:          make(chan struct{}),
			release:          make(chan struct{}),
			finished:         make(chan struct{}),
		}
		base := testPreparer()
		started := make(chan struct{})
		preparer := ReleasePreparerFunc{
			PrepareFunc: func(ctx context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
				close(started)
				<-ctx.Done()
				return api.PrepareResult{}, ctx.Err()
			},
			DisplayFunc:   base.ResolveDisplay,
			SubjectFunc:   base.ResolveUploadSubject,
			DuplicateFunc: base.ResolveDuplicateSubject,
		}
		m, err := New(repo, NewMemoryPrivateResourceStore(), preparer)
		if err != nil {
			t.Fatal(err)
		}
		created := executeCommand(t, m, CreateWorkflowCommand{})
		var borrowers atomic.Int32
		m.SetOperationLifetime(func() (func(), bool) {
			borrowers.Add(1)
			return func() { borrowers.Add(-1) }, true
		})
		_, err = m.Start(t.Context(), testOwnerID, PrepareReleaseCommand{
			WorkflowID:       created.Workflow.ID,
			ExpectedRevision: created.Workflow.Revision,
			Input:            api.PrepareInput{SourcePath: "Synthetic.Film.2026.mkv"},
			IdempotencyKey:   "review-lease",
		})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		time.Sleep(workflowWorkHeartbeat + time.Nanosecond)
		synctest.Wait()
		select {
		case <-repo.entered:
		default:
			t.Fatal("actual operation lease did not start renewal")
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err = m.Shutdown(ctx)
		select {
		case <-repo.finished:
			t.Fatal("renewal unexpectedly finished")
		default:
		}
		if !errors.Is(err, context.DeadlineExceeded) || borrowers.Load() != 1 {
			t.Fatalf("unfinished renewal shutdown = %v, runtime borrowers = %d", err, borrowers.Load())
		}
		close(repo.release)
		<-repo.finished
		if err := m.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if borrowers.Load() != 0 {
			t.Fatal("joined operation retained its runtime borrower")
		}
	})
}

type recoveryCloseRepository struct {
	*MemoryRepository
	closed, afterClose atomic.Bool
	block              atomic.Bool
	entered, release   chan struct{}
}

func (r *recoveryCloseRepository) LoadWork(ctx context.Context, owner string, workflow api.WorkflowID, operation api.WorkflowOperationID) (api.ReleaseWorkflowWorkRecord, error) {
	if r.block.Swap(false) {
		close(r.entered)
		<-r.release // Exercise recovery already inside a dependency during shutdown.
	}
	if r.closed.Load() {
		r.afterClose.Store(true)
		return api.ReleaseWorkflowWorkRecord{}, errors.New("synthetic repository already closed")
	}
	return r.MemoryRepository.LoadWork(ctx, owner, workflow, operation)
}
func TestCoordinatorShutdownCancelsActualRecoveryTimer(t *testing.T) {
	for _, state := range []string{"pending", "inflight", "extended lease"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				base := testPreparer()
				started := make(chan struct{})
				preparer := ReleasePreparerFunc{
					PrepareFunc: func(ctx context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
						close(started)
						<-ctx.Done()
						return api.PrepareResult{}, ctx.Err()
					},
					DisplayFunc:   base.ResolveDisplay,
					SubjectFunc:   base.ResolveUploadSubject,
					DuplicateFunc: base.ResolveDuplicateSubject,
				}
				repo := &recoveryCloseRepository{
					MemoryRepository: NewMemoryRepository(),
					entered:          make(chan struct{}),
					release:          make(chan struct{}),
				}
				first, err := New(repo, NewMemoryPrivateResourceStore(), preparer)
				if err != nil {
					t.Fatal(err)
				}
				created := executeCommand(t, first, CreateWorkflowCommand{})
				operation, err := first.Start(t.Context(), testOwnerID, PrepareReleaseCommand{
					WorkflowID:       created.Workflow.ID,
					ExpectedRevision: created.Workflow.Revision,
					Input:            api.PrepareInput{SourcePath: "Synthetic.Film.2026.mkv"},
					IdempotencyKey:   "review-recovery",
				})
				if err != nil {
					t.Fatal(err)
				}
				<-started
				record, err := repo.LoadOperation(t.Context(), testOwnerID, created.Workflow.ID, operation.ID)
				if err != nil {
					t.Fatal(err)
				}
				work, err := repo.LoadWork(t.Context(), testOwnerID, created.Workflow.ID, operation.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := first.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				// Restore the valid producer's receipt/lease as a simulated process crash.
				repo.mu.Lock()
				repo.operations[operation.ID] = record
				repo.work[memoryWorkKey(testOwnerID, created.Workflow.ID, operation.ID)] = work
				repo.mu.Unlock()
				second, err := New(repo, NewMemoryPrivateResourceStore(), base)
				if err != nil {
					t.Fatal(err)
				}
				var borrowers atomic.Int32
				second.SetOperationLifetime(func() (func(), bool) {
					borrowers.Add(1)
					return func() { borrowers.Add(-1) }, true
				})
				if err := second.ensureOperationRecovery(t.Context()); err != nil {
					t.Fatal(err)
				}
				switch state {
				case "inflight":
					repo.block.Store(true)
					time.Sleep(workflowWorkLeaseTTL + time.Nanosecond)
					<-repo.entered
					ctx, cancel := context.WithTimeout(t.Context(), time.Second)
					err := second.Shutdown(ctx)
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) || borrowers.Load() != 1 {
						t.Fatalf("unfinished recovery shutdown = %v, runtime borrowers = %d", err, borrowers.Load())
					}
					close(repo.release)
				case "extended lease":
					repo.mu.Lock()
					work.LeaseExpiresAt = work.LeaseExpiresAt.Add(workflowWorkLeaseTTL)
					repo.work[memoryWorkKey(testOwnerID, created.Workflow.ID, operation.ID)] = work
					repo.mu.Unlock()
					time.Sleep(workflowWorkLeaseTTL + time.Nanosecond)
					synctest.Wait()
					if borrowers.Load() != 1 {
						t.Fatal("rescheduled recovery did not retain the runtime borrower")
					}
				}
				if err := second.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				if borrowers.Load() != 0 {
					t.Fatal("joined recovery retained its runtime borrower")
				}
				repo.closed.Store(true)
				time.Sleep(workflowWorkLeaseTTL + time.Nanosecond)
				synctest.Wait()
				if repo.afterClose.Load() {
					t.Error("actual prior-process recovery timer used repository after Shutdown returned nil and close")
				}
			})
		})
	}
}
