// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestRecoverLegacyInputExposesOnlyReconciliationAndReopensAfterClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &hashingActiveInputVerifier{}
	module := newActiveInputRecoveryModule(t, persistent, repo, verifier, clock, "legacy-recovery")
	created, err := module.Execute(ctx, testOwnerID, CreateWorkflowCommand{IdempotencyKey: "legacy-workflow"})
	if err != nil {
		t.Fatalf("create legacy workflow: %v", err)
	}
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          created.Workflow.ID,
		OperationID:         "legacy-operation",
		EffectID:            "legacy-effect",
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "PTP",
		SemanticFingerprint: "legacy-submission",
		StartedAt:           clock.Now(),
		UpdatedAt:           clock.Now(),
	}
	if _, _, err := persistent.BeginEffect(ctx, effect); err != nil {
		t.Fatalf("begin legacy effect: %v", err)
	}

	slot, err := module.RecoverLegacyInput(ctx, testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("recover legacy input: %v", err)
	}
	if !IsLegacyRecoverySlot(slot) || slot.OwnerID != testOwnerID || slot.WorkflowID != created.Workflow.ID {
		t.Fatalf("legacy recovery slot = %#v", slot)
	}
	if _, err := module.Execute(ctx, testOwnerID, CancelWorkflowCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		IdempotencyKey:   "not-reconciliation",
	}); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("non-reconciliation command in recovery = %v", err)
	}
	current, err := module.Current(ctx, testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("current legacy workflow: %v", err)
	}
	if len(current.Workflow.RequiredActions) != 1 {
		t.Fatalf("legacy recovery actions = %#v", current.Workflow.RequiredActions)
	}
	action := current.Workflow.RequiredActions[0]
	if action.Kind != api.RequiredActionReconcileSubmission || action.EffectKind != api.WorkflowExternalEffectTrackerSubmission ||
		action.EffectScopeID != "PTP" || action.TrackerID != "PTP" {
		t.Fatalf("legacy recovery action = %#v", action)
	}
	resolved, err := module.Execute(ctx, testOwnerID, ResolveActionCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		IdempotencyKey:   "resolve-legacy-effect",
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: current.Workflow.Revision,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
	})
	if err != nil {
		t.Fatalf("resolve legacy action: %v", err)
	}
	if len(resolved.Workflow.RequiredActions) != 0 {
		t.Fatalf("resolved legacy actions = %#v", resolved.Workflow.RequiredActions)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != api.ActiveInputEmpty {
		t.Fatalf("legacy slot after reconciliation = %#v", closed)
	}
}

func TestFinishLegacyInputRecoverySerializesHeartbeatCancellation(t *testing.T) {
	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repo := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	module := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "legacy-recovery")
	created, err := module.Execute(ctx, testOwnerID, CreateWorkflowCommand{IdempotencyKey: "legacy-workflow"})
	if err != nil {
		t.Fatalf("create legacy workflow: %v", err)
	}
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := api.ActiveInputRecord{
		State:          api.ActiveInputRecovering,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        testOwnerID,
		CoordinatorID:  module.processEpoch,
		WorkflowID:     created.Workflow.ID,
		LeaseExpiresAt: clock.Now().Add(workflowWorkLeaseTTL),
	}
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          created.Workflow.ID,
		OperationID:         "legacy-operation",
		EffectID:            "legacy-effect",
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "PTP",
		SemanticFingerprint: "legacy-submission",
		StartedAt:           clock.Now(),
		UpdatedAt:           clock.Now(),
	}
	if _, _, err := persistent.BeginEffect(ctx, effect); err != nil {
		t.Fatalf("begin legacy effect: %v", err)
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, legacy, clock.Now()); err != nil {
		t.Fatal(err)
	}
	recoveryCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: legacy.CoordinatorID, Fence: legacy.Fence})
	completedAt := clock.Now()
	effect.UpdatedAt, effect.CompletedAt = completedAt, &completedAt
	if err := persistent.CompleteEffect(recoveryCtx, api.WorkflowEffectStatusSucceeded, effect); err != nil {
		t.Fatalf("complete legacy effect: %v", err)
	}

	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	heartbeatDone := make(chan struct{})
	finishResult := make(chan error, 1)
	go func() {
		ready <- struct{}{}
		<-start
		module.activeMu.Lock()
		module.startActiveInputHeartbeat(recoveryCtx, legacy.Fence)
		module.activeMu.Unlock()
		close(heartbeatDone)
	}()
	go func() {
		ready <- struct{}{}
		<-start
		finishResult <- module.finishLegacyInputRecovery(recoveryCtx, testOwnerID, legacy.WorkflowID)
	}()
	<-ready
	<-ready
	close(start)
	<-heartbeatDone
	if err := <-finishResult; err != nil {
		t.Fatalf("finish legacy recovery: %v", err)
	}
	closed, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != api.ActiveInputEmpty {
		t.Fatalf("legacy recovery slot = %#v", closed)
	}
}
