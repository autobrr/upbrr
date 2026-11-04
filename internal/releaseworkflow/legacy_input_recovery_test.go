// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
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
	for _, backend := range []string{"memory", "sqlite"} {
		for _, test := range []struct {
			name, receiptID                      string
			operationID                          api.WorkflowOperationID
			failSave, legacyAction, cleanReceipt bool
		}{
			{name: "accepted", receiptID: "accepted-effect"},
			{
				name:         "accepted-clean-receipt",
				receiptID:    "accepted-effect",
				cleanReceipt: true,
			},
			{name: "different-attempt", receiptID: "different-effect"},
			{
				name:        "different-operation",
				receiptID:   "accepted-effect",
				operationID: "different-operation",
			},
			{
				name:      "retry-after-save-failure",
				receiptID: "accepted-effect",
				failSave:  true,
			},
			{
				name:         "retry-unbound-after-save-failure",
				receiptID:    "accepted-effect",
				failSave:     true,
				legacyAction: true,
			},
		} {
			t.Run(backend+"/"+test.name, func(t *testing.T) {
				receiptID := test.receiptID
				ctx := t.Context()
				clock := &mutableClock{now: time.Now().UTC()}
				var module *Module
				var repo Repository
				var database *db.SQLiteRepository
				if backend == "sqlite" {
					database = openActiveInputRecoveryRepository(ctx, t)
					persistent, err := NewPersistentRepository(database)
					if err != nil {
						t.Fatal(err)
					}
					repo = persistent
					module = newActiveInputRecoveryModule(t, persistent, database, &hashingActiveInputVerifier{}, clock, "accepted-recovery")
				} else {
					var memory *MemoryRepository
					module, memory = newTestModule(t, testPreparer(), WithClock(clock))
					repo = memory
				}
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
				if _, _, err := module.durability.BeginEffect(ctx, effect); err != nil {
					t.Fatal(err)
				}
				if err := module.durability.MarkOperationEffectsUnknown(ctx, testOwnerID, created.Workflow.ID, effect.OperationID, now); err != nil {
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
				operationID := test.operationID
				if operationID == "" {
					operationID = effect.OperationID
				}
				accepted := receiptID == effect.EffectID && operationID == effect.OperationID
				result := api.UploadTrackerResult{
					TrackerID:             "EXAMPLE",
					Status:                api.StageStatusPartial,
					SubmissionStatus:      api.StageStatusCompleted,
					ClientInjectionStatus: api.StageStatusUnavailable,
					SubmissionReceiptID:   receiptID,
					SubmissionOperationID: operationID,
					Failures: []api.WorkflowFailure{{
						TrackerID: "EXAMPLE",
						Resource:  receiptID,
						Failure: api.OperationFailure{
							Code:      api.OperationFailureReceiptRetention,
							Operation: api.OperationKindUploadExecute,
							Recovery:  api.OperationRecoveryNone,
							Message:   "Synthetic receipt failure.",
						},
					}},
				}
				if test.cleanReceipt {
					result.Status, result.ClientInjectionStatus = api.StageStatusCompleted, api.StageStatusSkipped
					result.Failures = nil
				}
				publication, err := module.publishUploadResult(ctx, testOwnerID, &state, state.Workflow.Revision, now, preparedUploads{
					projections:  api.TrackerReleaseProjectionSet{ID: "projections", Revision: 1},
					dupes:        api.DupeAssessment{ID: "dupes", Revision: 1},
					media:        api.MediaArtifactSet{ID: "media", Revision: 1},
					descriptions: api.DescriptionSet{ID: "descriptions", Revision: 1},
					plan:         api.UploadPlan{InputFingerprint: fingerprint},
				}, []api.UploadTrackerResult{result}, RegisteredArtifactAuthority{Torrents: map[api.TrackerID]api.TorrentResult{"EXAMPLE": {URL: "https://tracker.invalid/download/123?passkey=synthetic-secret"}}})
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
				if _, err := module.private.Get(testOwnerID, created.Workflow.ID, registeredArtifactAuthorityPrivateResourceID(resultID), now); err != nil && !test.cleanReceipt {
					t.Fatalf("retained accepted artifact: %v", err)
				}
				action, err := module.newReconcileAction(state.Workflow.Revision, now, "EXAMPLE", api.WorkflowExternalEffectTrackerSubmission, effect.ScopeID, "Verify the interrupted attempt.")
				if err != nil {
					t.Fatal(err)
				}
				if !test.legacyAction {
					action.EffectID, action.EffectOperationID = effect.EffectID, effect.OperationID
				}
				state.Workflow.RequiredActions = []api.RequiredAction{action}
				state.Workflow.Status = api.WorkflowStatusBlocked
				if err := repo.Save(ctx, testOwnerID, previous, state); err != nil {
					t.Fatal(err)
				}
				unproven := effect
				unproven.OperationID, unproven.CompletedAt = "different-operation", &now
				if err := module.durability.CompleteEffect(ctx, api.WorkflowEffectStatusSucceeded, unproven); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
					t.Fatalf("different operation completed the unknown effect: %v", err)
				}
				unproven.OperationID = effect.OperationID
				if err := module.durability.CompleteEffect(ctx, api.WorkflowEffectStatusFailed, unproven); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
					t.Fatalf("unknown effect was completed as failed: %v", err)
				}
				if accepted {
					if module.activeInputs != nil {
						if err := module.durability.ResolveEffectUnknown(ctx, testOwnerID, created.Workflow.ID,
							api.WorkflowExternalEffectTrackerSubmission, effect.ScopeID, now,
							api.WorkflowExternalEffectReceipt{EffectID: effect.EffectID, OperationID: effect.OperationID}); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
							t.Fatalf("accepted durable confirmation=%v", err)
						}
					} else if _, err := module.Execute(ctx, testOwnerID, ResolveActionCommand{
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
				}
				if test.failSave {
					failedCtx, cancel := context.WithCancel(ctx)
					durability := module.durability
					module.durability = cancelAfterLegacyEffectCompletion{DurabilityRepository: durability, cancel: cancel}
					var err error
					if module.activeInputs != nil {
						_, err = module.RecoverLegacyInput(failedCtx, testOwnerID, created.Workflow.ID)
					} else {
						err = module.publishLegacyRecoveryActions(failedCtx, testOwnerID, created.Workflow.ID, []api.ReleaseWorkflowEffectRecord{effect})
					}
					module.durability = durability
					cancel()
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("save after confirmed effect completion = %v", err)
					}
				}
				if module.activeInputs != nil {
					slot, err := module.RecoverLegacyInput(ctx, testOwnerID, created.Workflow.ID)
					if err != nil {
						t.Fatal(err)
					}
					wantState := api.ActiveInputEmpty
					if !accepted {
						wantState = api.ActiveInputRecovering
					}
					if slot.State != wantState {
						t.Fatalf("accepted legacy recovery retained input claim: %#v", slot)
					}
				} else if test.failSave {
					settled, err := module.settleRecoveryActions(ctx, testOwnerID, created.Workflow.ID)
					if err != nil || !settled {
						t.Fatalf("settle completed legacy effect: settled=%v error=%v", settled, err)
					}
				} else if err := module.publishLegacyRecoveryActions(ctx, testOwnerID, created.Workflow.ID, []api.ReleaseWorkflowEffectRecord{effect}); err != nil {
					t.Fatal(err)
				}
				state, err = repo.Load(ctx, testOwnerID, created.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !accepted {
					// Another attempt's acceptance cannot suppress genuine uncertainty.
					if len(state.Workflow.RequiredActions) != 1 || state.Workflow.RequiredActions[0].EffectID != effect.EffectID ||
						state.Workflow.RequiredActions[0].EffectOperationID != effect.OperationID {
						t.Fatalf("unproven attempt recovery=%#v", state.Workflow.RequiredActions)
					}
					ids, err := module.durability.ListLegacyRecoveryWorkflowIDs(ctx, testOwnerID)
					if err != nil || len(ids) != 1 || ids[0] != created.Workflow.ID {
						t.Fatalf("unproven attempt lost its recovery fence: ids=%v error=%v", ids, err)
					}
					return
				}
				if len(state.Workflow.RequiredActions) != 0 {
					t.Fatalf("accepted attempt offered not-completed: %#v", state.Workflow.RequiredActions)
				}
				if test.cleanReceipt && len(state.Workflow.Failures) != 0 {
					t.Fatalf("clean accepted receipt acquired a recovery failure: %#v", state.Workflow.Failures)
				}
				if state.Workflow.Status != api.WorkflowStatusCompleted {
					t.Fatalf("accepted attempt left workflow status %s", state.Workflow.Status)
				}
				if state.Workflow.UploadResult == nil || state.Workflow.UploadResult.ID != resultID || !state.UploadResults[resultID].ConfirmsSubmission(effect) {
					t.Fatal("legacy recovery lost retained acceptance")
				}
				retainedResult := state.UploadResults[resultID].Results[0]
				if !slices.Equal(retainedResult.Failures, result.Failures) {
					t.Fatalf("legacy recovery changed retained receipt failures: %#v", retainedResult.Failures)
				}
				if retainedResult.SubmissionRetryable() || retainedResult.ClientInjectionRetryable() {
					t.Fatal("legacy recovery reopened an accepted receipt-retention attempt")
				}
				if _, err := module.private.Get(testOwnerID, created.Workflow.ID, registeredArtifactAuthorityPrivateResourceID(resultID), clock.Now()); err != nil && !test.cleanReceipt {
					t.Fatalf("legacy recovery lost registered authority: %v", err)
				}
				ids, err := module.durability.ListLegacyRecoveryWorkflowIDs(ctx, testOwnerID)
				if err != nil || len(ids) != 0 {
					t.Fatalf("accepted attempt still requires legacy recovery: ids=%v error=%v", ids, err)
				}
				if database != nil {
					var status api.WorkflowEffectStatus
					if err := database.RawDB().QueryRowContext(ctx,
						`SELECT status FROM release_workflow_effects WHERE owner_id = ? AND workflow_id = ? AND operation_id = ? AND effect_id = ?`,
						testOwnerID, created.Workflow.ID, effect.OperationID, effect.EffectID).Scan(&status); err != nil || status != api.WorkflowEffectStatusSucceeded {
						t.Fatalf("accepted effect was not durably retained as successful: status=%s error=%v", status, err)
					}
				} else {
					retained, idempotent, err := module.durability.BeginEffect(ctx, effect)
					if err != nil || !idempotent || retained.Status != api.WorkflowEffectStatusSucceeded || retained.EffectID != effect.EffectID {
						t.Fatalf("accepted effect was not retained as successful: effect=%#v idempotent=%v error=%v", retained, idempotent, err)
					}
				}
			})
		}
	}
}

type cancelAfterLegacyEffectCompletion struct {
	DurabilityRepository
	cancel context.CancelFunc
}

func (r cancelAfterLegacyEffectCompletion) CompleteEffect(ctx context.Context, status api.WorkflowEffectStatus, record api.ReleaseWorkflowEffectRecord) error {
	if err := r.DurabilityRepository.CompleteEffect(ctx, status, record); err != nil {
		return fmt.Errorf("complete legacy effect before cancellation: %w", err)
	}
	r.cancel()
	return nil
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
