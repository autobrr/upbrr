// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPersistentWorkflowRestartPreservesSafePreparedRelease(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "release-workflows.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	privateA := NewMemoryPrivateResourceStore()
	moduleA, err := New(
		persistentA,
		privateA,
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{
		WorkflowID:     "workflow-restart",
		IdempotencyKey: "create-restart",
	})
	prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		},
	})

	state, err := persistentA.Load(context.Background(), testOwnerID, prepared.Workflow.ID)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("load prepared state: %v", err)
	}
	state.Workflow.Revision = 3
	state.Workflow.Status = api.WorkflowStatusActive
	state.Workflow.UpdatedAt = now.Add(time.Minute)
	operationID := api.WorkflowOperationID("operation-before-restart")
	state.Operations[operationID] = api.WorkflowOperationStatus{
		ID:         operationID,
		WorkflowID: state.Workflow.ID,
		Revision:   3,
		Operation:  api.OperationKindUploadExecute,
		Status:     api.StageStatusRunning,
		Progress:   50,
		StartedAt:  now,
	}
	if err := persistentA.Save(context.Background(), testOwnerID, 2, state); err != nil {
		_ = repoA.Close()
		t.Fatalf("save reviewed state: %v", err)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(2 * time.Minute)}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}

	current, err := moduleB.Current(context.Background(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatalf("load restarted workflow: %v", err)
	}
	if current.Workflow.Revision != 4 || current.Workflow.Release == nil || current.Workflow.DryRun != nil || current.Workflow.UploadResult != nil {
		t.Fatalf("restarted workflow refs = %#v", current.Workflow)
	}
	if current.Workflow.Status != api.WorkflowStatusActive || len(current.Workflow.RequiredActions) != 0 {
		t.Fatalf("restarted workflow action = %#v", current.Workflow)
	}
	retained, err := persistentB.Load(context.Background(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatalf("load recovered durable state: %v", err)
	}
	if len(retained.Releases) != 1 {
		t.Fatalf("safe release audit snapshot was removed: releases=%d", len(retained.Releases))
	}
	operation, err := moduleB.Operation(context.Background(), testOwnerID, state.Workflow.ID, operationID)
	if err != nil {
		t.Fatalf("load interrupted operation: %v", err)
	}
	if operation.Status != api.StageStatusInterrupted || operation.CompletedAt == nil || len(operation.Failures) != 1 {
		t.Fatalf("interrupted operation = %#v", operation)
	}
	if _, err := moduleB.Workflow(context.Background(), "different-owner", state.Workflow.ID); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign-owner workflow error = %v", err)
	}

	_, err = moduleB.Execute(context.Background(), testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: 3,
		IdempotencyKey:   "old-upload-old-revision",
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("old revision execution error = %v", err)
	}
	_, err = moduleB.Execute(context.Background(), testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		IdempotencyKey:   "upload-before-reprepare",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("upload before reprepare error = %v", err)
	}
	reprepared := executeCommand(t, moduleB, PrepareReleaseCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		},
		IdempotencyKey: "fresh-preparation-after-restart",
	})
	if reprepared.Workflow.Release == nil || reprepared.Workflow.DryRun != nil || reprepared.Workflow.UploadResult != nil ||
		len(reprepared.Workflow.RequiredActions) != 0 || len(reprepared.Workflow.Failures) != 0 {
		t.Fatalf("reused preparation after restart = %#v", reprepared.Workflow)
	}
}

func TestPersistentWorkflowOperationRestartIsInterrupted(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "release-workflow-operation.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-operation-restart"})
	fingerprint, err := api.CanonicalWorkflowFingerprint(map[string]string{"command": "prepare_release"})
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("fingerprint operation: %v", err)
	}
	operationID := api.WorkflowOperationID("operation-restart")
	_, _, err = persistentA.CreateOperation(context.Background(), api.ReleaseWorkflowOperationRecord{
		OwnerID:            testOwnerID,
		WorkflowID:         created.Workflow.ID,
		OperationID:        operationID,
		ExpectedRevision:   created.Workflow.Revision,
		IdempotencyKey:     "prepare-restart",
		CommandFingerprint: fingerprint,
		ProcessEpoch:       "epoch-a",
		Status: api.WorkflowOperationStatus{
			ID:         operationID,
			WorkflowID: created.Workflow.ID,
			Revision:   created.Workflow.Revision,
			Sequence:   1,
			Command:    "prepare_release",
			Operation:  api.OperationKindPreparation,
			Status:     api.StageStatusRunning,
			StartedAt:  now,
			UpdatedAt:  now,
		},
	})
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("create running operation: %v", err)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Minute)}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	if _, err := moduleB.Current(context.Background(), testOwnerID, created.Workflow.ID); err != nil {
		t.Fatalf("load workflow after restart: %v", err)
	}
	operation, err := moduleB.Operation(context.Background(), testOwnerID, created.Workflow.ID, operationID)
	if err != nil {
		t.Fatalf("load interrupted operation: %v", err)
	}
	if operation.Status != api.StageStatusInterrupted || operation.Sequence != 2 || operation.CompletedAt == nil {
		t.Fatalf("interrupted durable operation = %#v", operation)
	}
}

func TestPersistentWorkflowSuccessfulOperationReplayRetainsValidResultAfterRestart(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "release-workflow-stale-replay.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-stale-replay"})
	command := PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP"),
		},
		IdempotencyKey: "prepare-stale-replay",
	}
	operation, err := moduleA.Start(context.Background(), testOwnerID, command)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("start preparation: %v", err)
	}
	terminal := waitForWorkflowOperation(t, moduleA, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCompleted
	})
	if terminal.Result == nil || terminal.Result.Kind != api.WorkflowOperationResultRelease {
		_ = repoA.Close()
		t.Fatalf("successful operation result = %#v", terminal.Result)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Minute)}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	replayed, err := moduleB.Start(context.Background(), testOwnerID, command)
	if err != nil {
		t.Fatalf("replay preparation after restart: %v", err)
	}
	if replayed.ID != operation.ID || replayed.Status != api.StageStatusCompleted || replayed.Result == nil ||
		replayed.Result.Kind != api.WorkflowOperationResultRelease || replayed.ResultRevision != 2 ||
		len(replayed.Failures) != 0 {
		t.Fatalf("valid replay = %#v", replayed)
	}
}
