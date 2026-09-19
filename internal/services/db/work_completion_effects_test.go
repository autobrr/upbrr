// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkCompletionAtomicallyFencesUnresolvedEffects(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	workflow := workflowStateRecordForTest("terminal-effects", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	if err := activateSubmissionFenceTestInput(ctx, repo, workflow, now); err != nil {
		t.Fatal(err)
	}
	ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "submission-fence-test", Fence: 1})
	operation := workflowOperationRecordForTest(t, workflow.OwnerID, workflow.WorkflowID, "submission-operation", now)
	if _, _, err := repo.CreateReleaseWorkflowOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	work := api.ReleaseWorkflowWorkRecord{
		OwnerID:        workflow.OwnerID,
		WorkflowID:     workflow.WorkflowID,
		OperationID:    "submission-operation",
		LeaseOwner:     "terminal-effects",
		LeaseExpiresAt: now.Add(time.Minute),
		UpdatedAt:      now,
		Checkpoint:     []byte(`{"status":"running"}`),
	}
	if err := repo.ClaimReleaseWorkflowWork(ctx, work); err != nil {
		t.Fatal(err)
	}
	succeeded := submissionFenceTestEffect(workflow, "success", submissionFenceTestIdentity(t, "a"), now)
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, succeeded); err != nil {
		t.Fatal(err)
	}
	completed := now.Add(time.Second)
	succeeded.UpdatedAt, succeeded.CompletedAt = completed, &completed
	if err := repo.CompleteReleaseWorkflowEffect(ctx, api.WorkflowEffectStatusSucceeded, succeeded); err != nil {
		t.Fatal(err)
	}
	uncertain := submissionFenceTestEffect(workflow, "uncertain", submissionFenceTestIdentity(t, "b"), completed)
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, uncertain); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `CREATE TRIGGER fail_terminal_effect
		BEFORE UPDATE OF status ON release_workflow_effects WHEN NEW.status = 'unknown'
		BEGIN SELECT RAISE(ABORT, 'forced terminal effect failure'); END`); err != nil {
		t.Fatal(err)
	}
	work.CompletedAt, work.UpdatedAt = &completed, completed
	work.Checkpoint = []byte(`{"status":"blocked"}`)
	if err := repo.CompleteReleaseWorkflowWork(ctx, work); err == nil {
		t.Fatal("expected effect write failure to abort terminal checkpoint")
	}
	stored, err := repo.LoadReleaseWorkflowWork(ctx, work.OwnerID, work.WorkflowID, work.OperationID)
	if err != nil || stored.CompletedAt != nil || string(stored.Checkpoint) != `{"status":"running"}` {
		t.Fatalf("failed completion changed work: %#v, err=%v", stored, err)
	}
	fence, err := repo.LoadSubmissionFence(ctx, uncertain.Submission.ContentIdentity, uncertain.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusStarted {
		t.Fatalf("failed completion changed fence: %#v, err=%v", fence, err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, "DROP TRIGGER fail_terminal_effect"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteReleaseWorkflowWork(ctx, work); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.LoadReleaseWorkflowWork(ctx, work.OwnerID, work.WorkflowID, work.OperationID)
	if err != nil || stored.CompletedAt == nil || string(stored.Checkpoint) != string(work.Checkpoint) {
		t.Fatalf("terminal work missing: %#v, err=%v", stored, err)
	}
	fence, err = repo.LoadSubmissionFence(ctx, uncertain.Submission.ContentIdentity, uncertain.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusUnknown {
		t.Fatalf("unresolved fence not unknown: %#v, err=%v", fence, err)
	}
	fence, err = repo.LoadSubmissionFence(ctx, succeeded.Submission.ContentIdentity, succeeded.Submission.TrackerSite)
	if err != nil || fence.Status != api.WorkflowEffectStatusSucceeded {
		t.Fatalf("successful fence changed: %#v, err=%v", fence, err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, workflow.OwnerID, workflow.WorkflowID,
		api.WorkflowExternalEffectTrackerSubmission, uncertain.ScopeID, completed.Add(time.Second)); err != nil {
		t.Fatalf("same-process reconciliation: %v", err)
	}
}
