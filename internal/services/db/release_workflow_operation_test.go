// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseWorkflowOperationPersistenceIsolationAndRetention(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "workflow-operation.sqlite")
	repo, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	workflow := workflowStateRecordForTest("workflow-operation", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatalf("create workflow state: %v", err)
	}
	record := workflowOperationRecordForTest(t, workflow.OwnerID, workflow.WorkflowID, "operation-1", now)
	created, idempotent, err := repo.CreateReleaseWorkflowOperation(ctx, record)
	if err != nil || idempotent || created.Status.Sequence != 1 {
		t.Fatalf("create operation = %#v, %v, %v", created, idempotent, err)
	}
	prior, idempotent, err := repo.CreateReleaseWorkflowOperation(ctx, record)
	if err != nil || !idempotent || prior.OperationID != record.OperationID {
		t.Fatalf("idempotent operation create = %#v, %v, %v", prior, idempotent, err)
	}
	if _, err := repo.LoadReleaseWorkflowOperation(ctx, "owner-2", workflow.WorkflowID, record.OperationID); !errors.Is(err, api.ErrReleaseWorkflowOperationNotFound) {
		t.Fatalf("foreign-owner operation load error = %v", err)
	}

	conflict := workflowOperationRecordForTest(t, workflow.OwnerID, workflow.WorkflowID, "operation-2", now)
	conflict.IdempotencyKey = "operation-key-2"
	if _, _, err := repo.CreateReleaseWorkflowOperation(ctx, conflict); !errors.Is(err, api.ErrReleaseWorkflowOperationConflict) {
		t.Fatalf("concurrent active operation error = %v", err)
	}

	running := record
	running.Status.Sequence = 2
	running.Status.Status = api.StageStatusRunning
	running.Status.Progress = 25
	running.Status.Message = "Checking trackers"
	running.Status.UpdatedAt = now.Add(time.Minute)
	if err := repo.SaveReleaseWorkflowOperation(ctx, 1, running); err != nil {
		t.Fatalf("save running operation: %v", err)
	}
	if err := repo.SaveReleaseWorkflowOperation(ctx, 1, running); !errors.Is(err, api.ErrReleaseWorkflowOperationSequenceConflict) {
		t.Fatalf("stale operation sequence error = %v", err)
	}
	active, err := repo.ListActiveReleaseWorkflowOperations(ctx)
	if err != nil || len(active) != 1 || active[0].Status.Message != running.Status.Message {
		t.Fatalf("active operations = %#v, %v", active, err)
	}

	if err := repo.Close(); err != nil {
		t.Fatalf("close repository before restart: %v", err)
	}
	repo, err = Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	loaded, err := repo.LoadLatestReleaseWorkflowOperation(ctx, workflow.OwnerID, workflow.WorkflowID)
	if err != nil || loaded.Status.Sequence != 2 || loaded.ProcessEpoch != record.ProcessEpoch {
		t.Fatalf("load operation after restart = %#v, %v", loaded, err)
	}

	completedAt := now.Add(2 * time.Minute)
	completed := loaded
	completed.Status.Sequence = 3
	completed.Status.Status = api.StageStatusCompleted
	completed.Status.Progress = 100
	completed.Status.CompletedAt = &completedAt
	completed.Status.UpdatedAt = completedAt
	if err := repo.SaveReleaseWorkflowOperation(ctx, 2, completed); err != nil {
		t.Fatalf("save completed operation: %v", err)
	}
	deleted, err := repo.DeleteTerminalReleaseWorkflowOperationsBefore(ctx, completedAt.Add(time.Minute))
	if err != nil || deleted != 1 {
		t.Fatalf("delete terminal operations = %d, %v", deleted, err)
	}
	if _, err := repo.LoadReleaseWorkflowOperation(ctx, workflow.OwnerID, workflow.WorkflowID, record.OperationID); !errors.Is(err, api.ErrReleaseWorkflowOperationNotFound) {
		t.Fatalf("terminal operation retained: %v", err)
	}
}

func workflowOperationRecordForTest(
	t *testing.T,
	ownerID string,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	now time.Time,
) api.ReleaseWorkflowOperationRecord {
	t.Helper()
	fingerprint, err := api.CanonicalWorkflowFingerprint(map[string]string{"command": "project_trackers"})
	if err != nil {
		t.Fatalf("fingerprint operation command: %v", err)
	}
	return api.ReleaseWorkflowOperationRecord{
		OwnerID:            ownerID,
		WorkflowID:         workflowID,
		OperationID:        operationID,
		ExpectedRevision:   1,
		IdempotencyKey:     "operation-key-1",
		CommandFingerprint: fingerprint,
		ProcessEpoch:       "process-epoch-1",
		Status: api.WorkflowOperationStatus{
			ID:         operationID,
			WorkflowID: workflowID,
			Revision:   1,
			Sequence:   1,
			Command:    "project_trackers",
			Operation:  api.OperationKindDryRun,
			Status:     api.StageStatusQueued,
			StartedAt:  now,
			UpdatedAt:  now,
		},
	}
}
