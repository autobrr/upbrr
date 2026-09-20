// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type failOnceOperationAdmissionRepository struct {
	*MemoryRepository
	failClaim      bool
	failEvents     bool
	failCheckpoint bool
	failAppendAt   int32
	claimed        atomic.Bool
	checkpointed   atomic.Bool
	events         atomic.Int32
}

func (r *failOnceOperationAdmissionRepository) ClaimWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.failClaim && !r.claimed.Swap(true) {
		return errors.New("synthetic work claim failure")
	}
	return r.MemoryRepository.ClaimWork(ctx, record)
}

func (r *failOnceOperationAdmissionRepository) CheckpointWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	if r.failCheckpoint && !r.checkpointed.Swap(true) {
		return errors.New("synthetic operation checkpoint failure")
	}
	return r.MemoryRepository.CheckpointWork(ctx, record)
}

func (r *failOnceOperationAdmissionRepository) AppendEvents(
	ctx context.Context,
	ownerID string,
	workflowID api.WorkflowID,
	events []api.WorkflowEvent,
) ([]api.WorkflowEvent, error) {
	if r.failEvents && r.events.Add(1) == r.failAppendAt {
		return nil, errors.New("synthetic queued event failure")
	}
	return r.MemoryRepository.AppendEvents(ctx, ownerID, workflowID, events)
}

type failTerminalOperationPublicationRepository struct {
	*MemoryRepository
	remaining atomic.Int32
}

func (r *failTerminalOperationPublicationRepository) SaveOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	if isTerminalProgressStatus(record.Status.Status) && r.remaining.Add(-1) >= 0 {
		return errors.New("synthetic terminal operation publication failure")
	}
	return r.MemoryRepository.SaveOperation(ctx, expectedSequence, record)
}

func TestOperationConvergesCompletedCheckpointAfterRepeatedPublicationFailures(t *testing.T) {
	t.Parallel()

	repository := &failTerminalOperationPublicationRepository{MemoryRepository: NewMemoryRepository()}
	repository.remaining.Store(2)
	module, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer())
	if err != nil {
		t.Fatalf("new module: %v", err)
	}
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-terminal-checkpoint-convergence"})
	operation, err := module.Start(t.Context(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "terminal-checkpoint-convergence",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		work, loadErr := repository.LoadWork(t.Context(), testOwnerID, created.Workflow.ID, operation.ID)
		if loadErr == nil && work.CompletedAt != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("terminal work checkpoint was not persisted")
		case <-time.After(time.Millisecond):
		}
	}
	stored, err := repository.LoadOperation(t.Context(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("load unpublished terminal operation: %v", err)
	}
	if !workflowOperationActive(stored.Status.Status) {
		t.Fatalf("operation before lazy convergence = %#v, want active", stored.Status)
	}

	status, err := module.Operation(t.Context(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("converge terminal operation from checkpoint: %v", err)
	}
	if status.Status != api.StageStatusCompleted || status.Result == nil || status.Result.Kind != api.WorkflowOperationResultRelease {
		t.Fatalf("converged terminal operation = %#v", status)
	}
}

func TestStartCompensatesPostCreateAdmissionFailure(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		failClaim  bool
		failEvents bool
	}{
		{name: "claim work", failClaim: true},
		{name: "append queued event", failEvents: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := &failOnceOperationAdmissionRepository{
				MemoryRepository: NewMemoryRepository(),
				failClaim:        testCase.failClaim,
				failEvents:       testCase.failEvents,
				failAppendAt:     1,
			}
			module, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer())
			if err != nil {
				t.Fatalf("new module: %v", err)
			}
			created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: api.WorkflowID("workflow-admission-" + testCase.name)})
			command := PrepareReleaseCommand{
				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "source"},
				IdempotencyKey:   "failed-admission",
			}
			if _, err := module.Start(t.Context(), testOwnerID, command); err == nil {
				t.Fatal("start with injected admission failure = nil")
			}
			active, err := repository.ListActiveOperations(t.Context())
			if err != nil {
				t.Fatalf("list active operations: %v", err)
			}
			if len(active) != 0 {
				t.Fatalf("admission failure left active operations: %#v", active)
			}
			failed, err := repository.LoadLatestOperation(t.Context(), testOwnerID, created.Workflow.ID)
			if err != nil {
				t.Fatalf("load compensated operation: %v", err)
			}
			if failed.Status.Status != api.StageStatusFailed || failed.Status.CompletedAt == nil {
				t.Fatalf("compensated operation = %#v", failed.Status)
			}

			command.IdempotencyKey = "retry-after-admission-failure"
			retry, err := module.Start(t.Context(), testOwnerID, command)
			if err != nil {
				t.Fatalf("retry operation: %v", err)
			}
			terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, retry.ID, func(status api.WorkflowOperationStatus) bool {
				return status.Status == api.StageStatusCompleted
			})
			if terminal.Status != api.StageStatusCompleted {
				t.Fatalf("retry terminal status = %#v", terminal)
			}
		})
	}
}

func TestRunOperationCompensatesRunningSetupFailure(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name           string
		failEvents     bool
		failCheckpoint bool
	}{
		{name: "checkpoint", failCheckpoint: true},
		{name: "event projection", failEvents: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := &failOnceOperationAdmissionRepository{
				MemoryRepository: NewMemoryRepository(),
				failEvents:       testCase.failEvents,
				failCheckpoint:   testCase.failCheckpoint,
				failAppendAt:     2,
			}
			module, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer())
			if err != nil {
				t.Fatalf("new module: %v", err)
			}
			created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: api.WorkflowID("workflow-running-admission-" + testCase.name)})

			command := PrepareReleaseCommand{
				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "source"},
				IdempotencyKey:   "running-publication-failure",
			}
			operation, err := module.Start(t.Context(), testOwnerID, command)
			if err != nil {
				t.Fatalf("start operation: %v", err)
			}
			terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
				return status.Status == api.StageStatusFailed
			})
			if terminal.Status != api.StageStatusFailed {
				t.Fatalf("terminal operation = %#v", terminal)
			}
			command.IdempotencyKey = "retry-after-running-publication-failure"
			retry, err := module.Start(t.Context(), testOwnerID, command)
			if err != nil {
				t.Fatalf("retry operation: %v", err)
			}
			if status := waitForWorkflowOperation(t, module, created.Workflow.ID, retry.ID, func(status api.WorkflowOperationStatus) bool {
				return status.Status == api.StageStatusCompleted
			}); status.Status != api.StageStatusCompleted {
				t.Fatalf("retry terminal operation = %#v", status)
			}
		})
	}
}

func TestStartCompositeAdmissionFailureClearsActiveOperation(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		failClaim  bool
		failEvents bool
	}{
		{name: "claim work", failClaim: true},
		{name: "append queued event", failEvents: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := &failOnceOperationAdmissionRepository{
				MemoryRepository: NewMemoryRepository(),
				failClaim:        testCase.failClaim,
				failEvents:       testCase.failEvents,
				failAppendAt:     1,
			}
			module, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer())
			if err != nil {
				t.Fatalf("new module: %v", err)
			}
			session := &compositeUploadSession{
				Version:            compositeUploadSessionVersion,
				RequestFingerprint: testFingerprint(t, "composite-admission-"+testCase.name),
				Goal:               api.WorkflowGoalDryRun,
			}
			created := executeCommand(t, module, CreateWorkflowCommand{
				WorkflowID: api.WorkflowID("workflow-composite-admission-" + testCase.name),
				Composite:  session,
			})
			command := CompositeUploadCommand{
				WorkflowID:         created.Workflow.ID,
				ExpectedRevision:   created.Workflow.Revision,
				SessionFingerprint: session.RequestFingerprint,
				Goal:               session.Goal,
				IdempotencyKey:     "failed-composite-admission",
			}
			if _, err := module.Start(t.Context(), testOwnerID, command); err == nil {
				t.Fatal("start composite with injected admission failure = nil")
			}
			state, err := repository.Load(t.Context(), testOwnerID, created.Workflow.ID)
			if err != nil {
				t.Fatalf("load composite state after admission failure: %v", err)
			}
			if state.Composite == nil || state.Composite.ActiveOperationID != "" ||
				state.Composite.TerminalReason != "admission_failed" {
				t.Fatalf("composite admission cleanup = %#v", state.Composite)
			}
			active, err := repository.ListActiveOperations(t.Context())
			if err != nil {
				t.Fatalf("list active operations: %v", err)
			}
			if len(active) != 0 {
				t.Fatalf("composite admission failure left active operations: %#v", active)
			}

			command.ExpectedRevision = state.Workflow.Revision
			command.IdempotencyKey = "retry-composite-admission"
			if _, err := module.Start(t.Context(), testOwnerID, command); err != nil {
				t.Fatalf("retry composite after admission cleanup: %v", err)
			}
		})
	}
}
