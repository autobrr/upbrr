// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// Keep the CLI session lifecycle and durable worker/input guards real; only
// source preparation is controlled so cancellation can precede worker exit.
type cliCancellationCore struct {
	*cliWorkflowActiveInputCore
	invocation context.Context
	observing  chan struct{}
	once       sync.Once
	releaseErr error
}

type cliCancellationCompletionCore struct {
	*cliWorkflowCoreFake
	status    api.StageStatus
	readErr   error
	cancelErr error
	interrupt context.CancelFunc
}

func (c *cliCancellationCompletionCore) CancelReleaseWorkflowOperation(ctx context.Context, _ string, _ api.WorkflowID,
	_ api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	c.cancelCalls++
	select {
	case <-ctx.Done():
		return api.WorkflowOperationStatus{}, fmt.Errorf("synthetic cancellation request: %w", ctx.Err())
	case <-time.After(4 * time.Second):
	}
	return api.WorkflowOperationStatus{Status: c.status}, c.cancelErr
}

func (c *cliCancellationCompletionCore) ReleaseWorkflowOperation(ctx context.Context, _ string, _ api.WorkflowID,
	_ api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	if c.interrupt != nil {
		c.interrupt()
		c.interrupt = nil
		return api.WorkflowOperationStatus{}, fmt.Errorf("synthetic status read: %w", ctx.Err())
	}
	return api.WorkflowOperationStatus{Status: c.status}, c.readErr
}

func (c *cliCancellationCompletionCore) ReleaseActiveInput(context.Context, string, api.ReleaseActiveInputRequest) (api.ActiveInputSnapshot, error) {
	return api.ActiveInputSnapshot{}, api.ErrActiveInputBusy
}

func TestCLICancellationCompletionBudgetAndRaces(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        api.StageStatus
		readErr       error
		cancelErr     error
		wantDeadline  bool
		interruptRead bool
	}{
		{
			name:         "worker remains active",
			status:       api.StageStatusRunning,
			wantDeadline: true,
		},
		{name: "normal completion wins", status: api.StageStatusCompleted},
		{
			name:    "completion read fails safely",
			status:  api.StageStatusRunning,
			readErr: errors.New("private-fixture path and token"),
		},
		{
			name:      "completed despite request failure",
			status:    api.StageStatusCompleted,
			cancelErr: errors.New("private-fixture request failure"),
		},
		{
			name:          "interrupted during status read",
			status:        api.StageStatusCompleted,
			interruptRead: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				operation := api.WorkflowOperationStatus{
					WorkflowID: "synthetic-workflow",
					ID:         "synthetic-operation",
					Operation:  api.OperationKindPreparation,
					Status:     api.StageStatusRunning,
				}
				core := &cliCancellationCompletionCore{
					cliWorkflowCoreFake: &cliWorkflowCoreFake{
						activeInput: api.ActiveInputSnapshot{
							State:    api.ActiveInputActive,
							Revision: 7,
							Current:  &api.ReleaseWorkflowCurrent{Workflow: api.ReleaseWorkflow{ID: operation.WorkflowID}},
						},
					},
					status:    test.status,
					readErr:   test.readErr,
					cancelErr: test.cancelErr,
				}
				logger := &cliProgressTestLogger{}
				session := &cliWorkflowSession{
					core:           core,
					logger:         logger,
					progressWriter: io.Discard,
					inputClaim:     cliInputSlotClaim{workflowID: operation.WorkflowID, revision: 7},
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if test.interruptRead {
					core.interrupt = cancel
				} else {
					cancel()
				}
				before := time.Now()
				_, err := session.waitForOperation(ctx, operation)
				if !errors.Is(err, context.Canceled) || core.cancelCalls != 1 {
					t.Fatalf("interruption lost: %v; requests=%d", err, core.cancelCalls)
				}
				if errors.Is(err, context.DeadlineExceeded) != test.wantDeadline {
					t.Fatalf("cleanup timeout cause: %v", err)
				}
				if test.wantDeadline && time.Since(before) != 5*time.Second {
					t.Fatalf("request and readback did not share a bounded budget: %v", time.Since(before))
				}
				if strings.Contains(err.Error(), "private-fixture") || (test.readErr != nil && !errors.Is(err, test.readErr)) {
					t.Fatalf("private cause exposure or loss: %v", err)
				}
				if test.status == api.StageStatusCompleted {
					expected := 4 * time.Second
					if test.interruptRead {
						expected += 200 * time.Millisecond
					}
					if _, failed := api.AsOperationFailure(err); failed || time.Since(before) != expected {
						t.Fatalf("terminal completion was treated as unconfirmed: %v, elapsed=%v", err, time.Since(before))
					}
				}
				if test.wantDeadline {
					session.releaseActiveInput(ctx)
					if core.activeInput.State != api.ActiveInputActive || !strings.Contains(strings.Join(logger.snapshot(), "\n"), "reason=work_active action=wait_then_retry") {
						t.Fatalf("active input or safe recovery diagnostic lost: %v", logger.snapshot())
					}
				}
			})
		})
	}
}

func (c *cliCancellationCore) ReleaseActiveInput(ctx context.Context, owner string, request api.ReleaseActiveInputRequest) (api.ActiveInputSnapshot, error) {
	if c.releaseErr != nil {
		return api.ActiveInputSnapshot{}, c.releaseErr
	}
	if err := c.module.ReleaseInput(ctx, owner, request.ExpectedRevision); err != nil {
		return api.ActiveInputSnapshot{}, fmt.Errorf("test release input: %w", err)
	}
	return c.GetActiveInput(ctx, owner)
}

func (c *cliCancellationCore) CancelReleaseWorkflowOperation(ctx context.Context, owner string, workflow api.WorkflowID,
	operation api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	status, err := c.module.CancelOperation(ctx, owner, workflow, operation)
	if err != nil {
		return status, fmt.Errorf("test cancel operation: %w", err)
	}
	return status, nil
}

func (c *cliCancellationCore) ReleaseWorkflowOperation(ctx context.Context, owner string, workflow api.WorkflowID,
	operation api.WorkflowOperationID,
) (api.WorkflowOperationStatus, error) {
	if c.invocation.Err() != nil && ctx.Err() == nil {
		c.once.Do(func() { close(c.observing) })
	}
	status, err := c.module.Operation(ctx, owner, workflow, operation)
	if err != nil {
		return status, fmt.Errorf("test read operation: %w", err)
	}
	return status, nil
}

func TestCLICancellationWaitsForDurableCleanup(t *testing.T) {
	t.Parallel()
	for _, releaseFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("release_failure=%t", releaseFailure), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, acknowledged, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var finishOnce sync.Once
			unblock := func() { finishOnce.Do(func() { close(finish) }) }
			repository, err := db.Open(filepath.Join(t.TempDir(), "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repository.Close() })
			if err := repository.Migrate(); err != nil {
				t.Fatal(err)
			}
			persistent, err := releaseworkflow.NewPersistentRepository(repository)
			if err != nil {
				t.Fatal(err)
			}
			logger := &cliProgressTestLogger{}
			module, err := releaseworkflow.New(persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{
				PrepareFunc: func(worker context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
					close(started)
					<-worker.Done()
					close(acknowledged)
					<-finish
					return api.PrepareResult{}, fmt.Errorf("synthetic preparation canceled: %w", worker.Err())
				},
			}, releaseworkflow.WithActiveInputs(repository, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
				return api.InputRecord{
					CanonicalPath: input.SourcePath,
					SourceVersion: "synthetic",
					Manifest:      []byte(`{"identity":{"digest":"synthetic"}}`),
				}, nil
			}), releaseworkflow.WithLogger(logger))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				unblock()
				shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := module.Shutdown(shutdown); err != nil {
					t.Error(err)
				}
			})
			core := &cliCancellationCore{
				cliWorkflowActiveInputCore: &cliWorkflowActiveInputCore{cliWorkflowCoreFake: &cliWorkflowCoreFake{}, module: module},
				invocation:                 ctx,
				observing:                  make(chan struct{}),
			}
			if releaseFailure {
				core.releaseErr = errors.New("synthetic storage failure path=private-fixture token=private-fixture")
			}
			result := make(chan error, 1)
			go func() {
				_, err := newCLIWorkflowSession(ctx, core, api.Request{SourcePath: filepath.Join(t.TempDir(), "Synthetic.Release.mkv")},
					api.PreparationIntentUpload, bufio.NewReader(strings.NewReader("")), config.Config{}, cliIO{out: io.Discard}, logger)
				result <- err
			}()
			select {
			case <-started:
			case err := <-result:
				t.Fatalf("CLI ended before preparation: %v; diagnostics=%v", err, logger.snapshot())
			case <-time.After(10 * time.Second):
				t.Fatal("preparation did not start")
			}
			cancel()
			select {
			case <-acknowledged:
			case <-time.After(10 * time.Second):
				t.Fatal("worker did not receive cancellation")
			}
			active, err := repository.LoadActiveInput(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := module.ReleaseInput(t.Context(), cliWorkflowOwnerID, active.Revision); !errors.Is(err, api.ErrActiveInputBusy) {
				t.Fatalf("busy input was not protected: %v", err)
			}
			select {
			case <-core.observing:
			case err := <-result:
				t.Fatalf("CLI returned before worker cleanup: %v; diagnostics=%v", err, logger.snapshot())
			case <-time.After(10 * time.Second):
				t.Fatal("CLI did not observe cancellation completion")
			}
			unblock()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("CLI cancellation cause lost: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("CLI did not finish after worker cleanup")
			}
			if releaseFailure {
				retained, err := repository.LoadActiveInput(t.Context())
				if err != nil || retained.State != api.ActiveInputActive || retained.WorkflowID != active.WorkflowID || retained.Revision != active.Revision {
					t.Fatalf("failed release lost recovery state: %#v err=%v", retained, err)
				}
				diagnostics := strings.Join(logger.snapshot(), "\n")
				if !strings.Contains(diagnostics, "stage=release_input state=cleanup_failed decision=recovery_required reason=cleanup_error action=restart_then_retry") || strings.Contains(diagnostics, "private-fixture") {
					t.Fatalf("unsafe or unactionable cleanup diagnostic: %s", diagnostics)
				}
				core.releaseErr = nil
				session := &cliWorkflowSession{
					core:       core,
					logger:     logger,
					inputClaim: cliInputSlotClaim{workflowID: retained.WorkflowID, revision: retained.Revision},
				}
				session.releaseActiveInput(ctx)
			}
			terminal, err := persistent.LoadLatestOperation(t.Context(), cliWorkflowOwnerID, active.WorkflowID)
			if err != nil || terminal.Status.Status != api.StageStatusCanceled {
				t.Fatalf("cancellation was not durable: status=%s err=%v", terminal.Status.Status, err)
			}
			work, err := persistent.LoadWork(t.Context(), cliWorkflowOwnerID, active.WorkflowID, terminal.OperationID)
			if err != nil || work.CompletedAt == nil {
				t.Fatalf("worker bookkeeping was not durable: %#v err=%v", work, err)
			}
			active, err = repository.LoadActiveInput(t.Context())
			if err != nil || active.State != api.ActiveInputEmpty {
				t.Fatalf("owned input was not released: state=%s err=%v diagnostics=%v", active.State, err, logger.snapshot())
			}
			if operations, err := persistent.ListActiveOperations(t.Context()); err != nil || len(operations) != 0 {
				t.Fatalf("durable operations remain active: %v, %v", operations, err)
			}
			if !releaseFailure && strings.Contains(strings.Join(logger.snapshot(), "\n"), "cleanup_failed") {
				t.Fatalf("avoidable cleanup warning: %v", logger.snapshot())
			}
		})
	}
}
