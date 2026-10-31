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
		action.EffectScopeID != "PTP" || action.TrackerID != "PTP" || action.EffectID != effect.EffectID || action.EffectOperationID != effect.OperationID {
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

func TestAcceptedReceiptSuppressesRecoveryAndRejectsDirectConfirmation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	module, repo := newTestModule(t, testPreparer())
	created, err := module.Execute(ctx, testOwnerID, CreateWorkflowCommand{IdempotencyKey: "accepted-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	now := module.clock.Now()
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          created.Workflow.ID,
		OperationID:         "operation",
		EffectID:            "accepted-effect",
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "EXAMPLE",
		SemanticFingerprint: "semantic",
		StartedAt:           now,
		UpdatedAt:           now,
	}
	if _, _, err := repo.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOperationEffectsUnknown(ctx, testOwnerID, created.Workflow.ID, effect.OperationID, now); err != nil {
		t.Fatal(err)
	}
	state, err := repo.Load(ctx, testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous := state.Workflow.Revision
	state.Workflow.Revision++
	fingerprint, err := api.CanonicalWorkflowFingerprint("receipt")
	if err != nil {
		t.Fatal(err)
	}
	publication, err := module.publishUploadResult(ctx, testOwnerID, &state, state.Workflow.Revision, now, preparedUploads{
		projections:  api.TrackerReleaseProjectionSet{ID: "projections", Revision: 1},
		dupes:        api.DupeAssessment{ID: "dupes", Revision: 1},
		media:        api.MediaArtifactSet{ID: "media", Revision: 1},
		descriptions: api.DescriptionSet{ID: "descriptions", Revision: 1},
		plan:         api.UploadPlan{InputFingerprint: fingerprint},
	}, []api.UploadTrackerResult{{
		TrackerID:             "EXAMPLE",
		Status:                api.StageStatusPartial,
		SubmissionStatus:      api.StageStatusCompleted,
		ClientInjectionStatus: api.StageStatusUnavailable,
		SubmissionReceiptID:   effect.EffectID,
		SubmissionOperationID: effect.OperationID,
		Failures: []api.WorkflowFailure{{
			TrackerID: "EXAMPLE",
			Resource:  effect.EffectID,
			Failure: api.OperationFailure{
				Code:      api.OperationFailureReceiptRetention,
				Operation: api.OperationKindUploadExecute,
				Recovery:  api.OperationRecoveryNone,
				Message:   "Synthetic receipt failure.",
			},
		}},
	}}, RegisteredArtifactAuthority{Torrents: map[api.TrackerID]api.TorrentResult{"EXAMPLE": {URL: "https://tracker.invalid/download/123?passkey=synthetic-secret"}}})
	if err != nil {
		t.Fatalf("publish accepted receipt failure: %v", err)
	}
	resultID := publication.UploadResult.ID
	state.Workflow.Release = &api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	state.Workflow.TrackerCatalog = &api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1}
	state.Workflow.TrackerRuntime = &api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1}
	state.Workflow.Selection = &api.TrackerSelectionRef{ID: "selection", Revision: 1}
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
	state.Workflow.Dupes = &api.DupeAssessmentRef{ID: "dupes", Revision: 1}
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: "media", Revision: 1}
	state.Workflow.Descriptions = &api.DescriptionSetRef{ID: "descriptions", Revision: 1}
	if _, err := module.private.Get(testOwnerID, created.Workflow.ID, registeredArtifactAuthorityPrivateResourceID(resultID), now); err != nil {
		t.Fatalf("retained accepted artifact: %v", err)
	}
	action, err := module.newReconcileAction(state.Workflow.Revision, now, "EXAMPLE", api.WorkflowExternalEffectTrackerSubmission, effect.ScopeID, "Verify the interrupted attempt.")
	if err != nil {
		t.Fatal(err)
	}
	action.EffectID, action.EffectOperationID = effect.EffectID, effect.OperationID
	state.Workflow.RequiredActions = []api.RequiredAction{action}
	state.Workflow.Status = api.WorkflowStatusBlocked
	if err := repo.Save(ctx, testOwnerID, previous, state); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Execute(ctx, testOwnerID, ResolveActionCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: state.Workflow.Revision,
		IdempotencyKey:   "stale-not-completed",
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: state.Workflow.Revision,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
	}); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("accepted direct confirmation=%v", err)
	}
	if err := module.publishLegacyRecoveryActions(ctx, testOwnerID, created.Workflow.ID, []api.ReleaseWorkflowEffectRecord{effect}); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Load(ctx, testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Workflow.RequiredActions) != 0 {
		t.Fatalf("accepted attempt offered not-completed: %#v", state.Workflow.RequiredActions)
	}
	// Acceptance for a different exact attempt cannot suppress genuine uncertainty.
	previous = state.Workflow.Revision
	state.Workflow.Revision++
	result := state.UploadResults[resultID]
	result.Results[0].SubmissionReceiptID = "another-attempt"
	state.UploadResults[resultID] = result
	if err := repo.Save(ctx, testOwnerID, previous, state); err != nil {
		t.Fatal(err)
	}
	if err := module.publishLegacyRecoveryActions(ctx, testOwnerID, created.Workflow.ID, []api.ReleaseWorkflowEffectRecord{effect}); err != nil {
		t.Fatal(err)
	}
	state, err = repo.Load(ctx, testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Workflow.RequiredActions) != 1 || state.Workflow.RequiredActions[0].EffectID != effect.EffectID || state.Workflow.RequiredActions[0].EffectOperationID != effect.OperationID {
		t.Fatalf("unproven attempt recovery=%#v", state.Workflow.RequiredActions)
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
		if err := module.startActiveInputHeartbeat(recoveryCtx, legacy.Fence); err != nil {
			t.Error(err)
		}
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
