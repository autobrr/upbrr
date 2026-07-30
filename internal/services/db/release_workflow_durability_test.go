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

func TestReleaseWorkflowDurabilitySurvivesRestart(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "workflow-durability.sqlite")
	repo, err := Open(databasePath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate repository: %v", err)
	}

	ctx := context.Background()
	now := time.Date(2026, time.July, 23, 10, 0, 0, 0, time.UTC)
	workflow := workflowStateRecordForTest("workflow-durability", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	operation := workflowOperationRecordForTest(t, workflow.OwnerID, workflow.WorkflowID, "operation-durability", now)
	if _, _, err := repo.CreateReleaseWorkflowOperation(ctx, operation); err != nil {
		t.Fatalf("create operation: %v", err)
	}

	intent := api.ReleaseWorkflowIntentRecord{
		OwnerID:            workflow.OwnerID,
		WorkflowID:         workflow.WorkflowID,
		IdempotencyKey:     "continue-1",
		RequestFingerprint: "intent-fingerprint",
		Goal:               api.WorkflowGoalUploaded,
		IntentPayload:      []byte(`{"goal":"uploaded"}`),
		AcceptedAt:         now,
	}
	if _, idempotent, err := repo.AcceptReleaseWorkflowIntent(ctx, intent); err != nil || idempotent {
		t.Fatalf("accept intent idempotent=%v err=%v", idempotent, err)
	}
	if _, idempotent, err := repo.AcceptReleaseWorkflowIntent(ctx, intent); err != nil || !idempotent {
		t.Fatalf("repeat intent idempotent=%v err=%v", idempotent, err)
	}
	conflict := intent
	conflict.RequestFingerprint = "different-fingerprint"
	if _, _, err := repo.AcceptReleaseWorkflowIntent(ctx, conflict); !errors.Is(err, api.ErrReleaseWorkflowIdempotencyConflict) {
		t.Fatalf("intent conflict error=%v", err)
	}

	if err := repo.SaveReleaseWorkflowContinuation(ctx, api.ReleaseWorkflowContinuationRecord{
		OwnerID:    workflow.OwnerID,
		WorkflowID: workflow.WorkflowID,
		Revision:   2,
		Payload:    []byte(`{"revision":2}`),
		UpdatedAt:  now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("save continuation: %v", err)
	}
	if err := repo.SaveReleaseWorkflowContinuation(ctx, api.ReleaseWorkflowContinuationRecord{
		OwnerID:    workflow.OwnerID,
		WorkflowID: workflow.WorkflowID,
		Revision:   1,
		Payload:    []byte(`{"revision":1}`),
		UpdatedAt:  now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("save stale continuation: %v", err)
	}
	var retainedRevision api.WorkflowRevision
	if err := repo.db.QueryRowContext(ctx, `
		SELECT revision
		FROM release_workflow_continuations
		WHERE owner_id = ? AND workflow_id = ?
	`, workflow.OwnerID, workflow.WorkflowID).Scan(&retainedRevision); err != nil {
		t.Fatalf("load continuation revision: %v", err)
	}
	if retainedRevision != 2 {
		t.Fatalf("retained continuation revision=%d, want 2", retainedRevision)
	}

	firstEvents, err := repo.AppendReleaseWorkflowEvents(ctx, workflow.OwnerID, workflow.WorkflowID, []api.WorkflowEvent{
		workflowEventForTest(workflow.WorkflowID, operation.OperationID, 1, now),
		workflowEventForTest(workflow.WorkflowID, operation.OperationID, 2, now.Add(time.Second)),
	})
	if err != nil {
		t.Fatalf("append events: %v", err)
	}
	if len(firstEvents) != 2 || firstEvents[0].Sequence != 1 || firstEvents[1].Sequence != 2 {
		t.Fatalf("first event cursors=%#v", firstEvents)
	}
	repeated, err := repo.AppendReleaseWorkflowEvents(ctx, workflow.OwnerID, workflow.WorkflowID, []api.WorkflowEvent{
		workflowEventForTest(workflow.WorkflowID, operation.OperationID, 1, now),
	})
	if err != nil || len(repeated) != 1 || repeated[0].Sequence != 1 {
		t.Fatalf("repeat event=%#v err=%v", repeated, err)
	}
	nextEvents, err := repo.AppendReleaseWorkflowEvents(ctx, workflow.OwnerID, workflow.WorkflowID, []api.WorkflowEvent{
		workflowEventForTest(workflow.WorkflowID, "operation-next", 1, now.Add(2*time.Second)),
	})
	if err != nil || len(nextEvents) != 1 || nextEvents[0].Sequence != 3 {
		t.Fatalf("next event=%#v err=%v", nextEvents, err)
	}

	work := api.ReleaseWorkflowWorkRecord{
		OwnerID:        workflow.OwnerID,
		WorkflowID:     workflow.WorkflowID,
		OperationID:    operation.OperationID,
		LeaseOwner:     "process-a",
		LeaseExpiresAt: now.Add(time.Minute),
		Checkpoint:     []byte(`{"sequence":1}`),
		UpdatedAt:      now,
	}
	if err := repo.ClaimReleaseWorkflowWork(ctx, work); err != nil {
		t.Fatalf("claim workflow work: %v", err)
	}
	competing := work
	competing.LeaseOwner = "process-b"
	competing.UpdatedAt = now.Add(30 * time.Second)
	competing.LeaseExpiresAt = now.Add(90 * time.Second)
	if err := repo.ClaimReleaseWorkflowWork(ctx, competing); !errors.Is(err, api.ErrReleaseWorkflowOperationConflict) {
		t.Fatalf("competing work claim error=%v", err)
	}
	work.UpdatedAt = now.Add(10 * time.Second)
	work.LeaseExpiresAt = now.Add(70 * time.Second)
	work.Checkpoint = []byte(`{"sequence":2}`)
	if err := repo.CheckpointReleaseWorkflowWork(ctx, work); err != nil {
		t.Fatalf("checkpoint workflow work: %v", err)
	}
	loadedWork, err := repo.LoadReleaseWorkflowWork(ctx, work.OwnerID, work.WorkflowID, work.OperationID)
	if err != nil {
		t.Fatalf("load checkpointed workflow work: %v", err)
	}
	if loadedWork.LeaseOwner != work.LeaseOwner ||
		!loadedWork.LeaseExpiresAt.Equal(work.LeaseExpiresAt) ||
		!loadedWork.UpdatedAt.Equal(work.UpdatedAt) ||
		string(loadedWork.Checkpoint) != string(work.Checkpoint) ||
		loadedWork.CompletedAt != nil {
		t.Fatalf("checkpointed workflow work=%#v, want %#v", loadedWork, work)
	}

	effect := workflowEffectForTest(workflow, operation.OperationID, "effect-1", "semantic-1", now)
	started, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, effect)
	if err != nil || idempotent || started.Status != api.WorkflowEffectStatusStarted {
		t.Fatalf("begin effect=%#v idempotent=%v err=%v", started, idempotent, err)
	}
	completedAt := now.Add(3 * time.Second)
	started.UpdatedAt = completedAt
	started.CompletedAt = &completedAt
	if err := repo.CompleteReleaseWorkflowEffect(ctx, api.WorkflowEffectStatusSucceeded, started); err != nil {
		t.Fatalf("complete effect: %v", err)
	}
	repeatedEffect := workflowEffectForTest(workflow, operation.OperationID, "effect-repeat", "semantic-1", completedAt)
	if prior, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, repeatedEffect); err != nil || !idempotent ||
		prior.EffectID != effect.EffectID {
		t.Fatalf("repeat effect=%#v idempotent=%v err=%v", prior, idempotent, err)
	}
	uncertain := workflowEffectForTest(workflow, operation.OperationID, "effect-2", "semantic-2", now.Add(4*time.Second))
	if _, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, uncertain); err != nil || idempotent {
		t.Fatalf("begin uncertain effect idempotent=%v err=%v", idempotent, err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(
		ctx,
		workflow.OwnerID,
		workflow.WorkflowID,
		operation.OperationID,
		now.Add(5*time.Second),
	); err != nil {
		t.Fatalf("mark effects unknown: %v", err)
	}

	if err := repo.Close(); err != nil {
		t.Fatalf("close repository: %v", err)
	}
	repo, err = Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}

	loadedWork, err = repo.LoadReleaseWorkflowWork(ctx, work.OwnerID, work.WorkflowID, work.OperationID)
	if err != nil {
		t.Fatalf("load workflow work after restart: %v", err)
	}
	if loadedWork.LeaseOwner != work.LeaseOwner ||
		!loadedWork.LeaseExpiresAt.Equal(work.LeaseExpiresAt) ||
		!loadedWork.UpdatedAt.Equal(work.UpdatedAt) ||
		string(loadedWork.Checkpoint) != string(work.Checkpoint) ||
		loadedWork.CompletedAt != nil {
		t.Fatalf("workflow work after restart=%#v, want %#v", loadedWork, work)
	}

	loadedEvents, err := repo.LoadReleaseWorkflowEvents(ctx, workflow.OwnerID, workflow.WorkflowID, 1, 10)
	if err != nil || len(loadedEvents) != 2 || loadedEvents[0].Sequence != 2 || loadedEvents[1].Sequence != 3 {
		t.Fatalf("events after restart=%#v err=%v", loadedEvents, err)
	}
	retry := workflowEffectForTest(workflow, operation.OperationID, "effect-3", "semantic-2", now.Add(6*time.Second))
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, retry); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("uncertain effect retry error=%v", err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(
		ctx,
		workflow.OwnerID,
		workflow.WorkflowID,
		api.WorkflowExternalEffectTrackerSubmission,
		"ALPHA",
		now.Add(7*time.Second),
	); err != nil {
		t.Fatalf("resolve uncertain effect: %v", err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(
		ctx,
		workflow.OwnerID,
		workflow.WorkflowID,
		api.WorkflowExternalEffectTrackerSubmission,
		"ALPHA",
		now.Add(8*time.Second),
	); err != nil {
		t.Fatalf("repeat uncertain effect resolution: %v", err)
	}
	if started, idempotent, err := repo.BeginReleaseWorkflowEffect(ctx, retry); err != nil || idempotent ||
		started.EffectID != retry.EffectID {
		t.Fatalf("retry reconciled effect=%#v idempotent=%v err=%v", started, idempotent, err)
	}
	work.UpdatedAt = now.Add(9 * time.Minute)
	work.LeaseExpiresAt = now.Add(10 * time.Minute)
	completedAt = work.UpdatedAt
	work.CompletedAt = &completedAt
	if err := repo.CompleteReleaseWorkflowWork(ctx, work); err != nil {
		t.Fatalf("complete workflow work after restart: %v", err)
	}
	loadedWork, err = repo.LoadReleaseWorkflowWork(ctx, work.OwnerID, work.WorkflowID, work.OperationID)
	if err != nil {
		t.Fatalf("load completed workflow work: %v", err)
	}
	if loadedWork.CompletedAt == nil || !loadedWork.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed workflow work=%#v, want completed_at=%s", loadedWork, completedAt)
	}
	if err := repo.ClaimReleaseWorkflowWork(ctx, competing); !errors.Is(err, api.ErrReleaseWorkflowOperationConflict) {
		t.Fatalf("completed work reclaim error=%v", err)
	}
}

func workflowEventForTest(
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	sequence uint64,
	timestamp time.Time,
) api.WorkflowEvent {
	return api.WorkflowEvent{
		Sequence:    sequence,
		WorkflowID:  workflowID,
		OperationID: operationID,
		Scope:       api.WorkflowEventScopeWorkflow,
		Lifecycle:   api.OperationLifecycleRunning,
		State:       api.StageStatusRunning,
		Disposition: api.WorkflowDispositionNone,
		Severity:    api.WorkflowEventSeverityInfo,
		Timestamp:   timestamp,
	}
}

func workflowEffectForTest(
	workflow api.ReleaseWorkflowStateRecord,
	operationID api.WorkflowOperationID,
	effectID string,
	fingerprint api.WorkflowFingerprint,
	now time.Time,
) api.ReleaseWorkflowEffectRecord {
	return api.ReleaseWorkflowEffectRecord{
		OwnerID:             workflow.OwnerID,
		WorkflowID:          workflow.WorkflowID,
		OperationID:         operationID,
		EffectID:            effectID,
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "ALPHA",
		SemanticFingerprint: fingerprint,
		Status:              api.WorkflowEffectStatusStarted,
		StartedAt:           now,
		UpdatedAt:           now,
	}
}
