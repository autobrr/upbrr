// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveImageCollectionReconciliationPreservesMediaAndOtherAttempts(t *testing.T) {
	t.Parallel()
	module, repository := newTestModule(t, testPreparer())
	ctx := t.Context()
	now := module.clock.Now()
	workflowID := api.WorkflowID("collection-recovery")
	first := recordUnknownCollectionEffect(ctx, t, repository, workflowID, "first", now)
	second := recordUnknownCollectionEffect(ctx, t, repository, workflowID, "second", now)
	actions := make([]api.RequiredAction, 0, 2)
	for _, effect := range []api.ReleaseWorkflowEffectRecord{first, second} {
		action, err := module.newReconcileAction(2, now, "", api.WorkflowExternalEffectImageHosting, effect.ScopeID, "")
		if err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	media := api.MediaArtifactSetRef{ID: "retained-media", Revision: 1}
	dupes := api.DupeAssessmentRef{ID: "retained-dupes", Revision: 1}
	secondActionID := actions[1].ID
	state := State{Workflow: api.ReleaseWorkflow{
		ID:              workflowID,
		Revision:        2,
		Status:          api.WorkflowStatusBlocked,
		Media:           &media,
		Dupes:           &dupes,
		Descriptions:    &api.DescriptionSetRef{ID: "obsolete-descriptions", Revision: 1},
		DryRun:          &api.UploadDryRunResultRef{ID: "obsolete-review", Revision: 1},
		RequiredActions: actions,
		Failures: []api.WorkflowFailure{
			collectionRecoveryFailure(first.ScopeID),
			collectionRecoveryFailure(second.ScopeID),
		},
	}}
	if err := module.private.Put(testOwnerID, workflowID, mediaPrivateResourceID(media.ID), "retained local media", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err := module.resolveAction(ctx, testOwnerID, &state, 3, now.Add(time.Second), ResolveActionCommand{
		WorkflowID:       workflowID,
		ExpectedRevision: 2,
		Answer: api.RequiredActionAnswer{
			ActionID:         actions[0].ID,
			WorkflowRevision: 2,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Workflow.Media == nil || *state.Workflow.Media != media || state.Workflow.Dupes == nil || *state.Workflow.Dupes != dupes ||
		state.Workflow.Descriptions != nil || state.Workflow.DryRun != nil || state.Workflow.Status != api.WorkflowStatusBlocked {
		t.Fatalf("collection reconciliation changed unrelated authority: %#v", state.Workflow)
	}
	if len(state.Workflow.RequiredActions) != 1 || state.Workflow.RequiredActions[0].ID != secondActionID ||
		len(state.Workflow.Failures) != 1 || state.Workflow.Failures[0].Resource != second.ScopeID {
		t.Fatalf("collection reconciliation cleared another attempt: %#v", state.Workflow)
	}
	if retained, err := module.private.Get(testOwnerID, workflowID, mediaPrivateResourceID(media.ID), now); err != nil || retained != "retained local media" {
		t.Fatalf("retained media = %v, error = %v", retained, err)
	}
	remaining, err := repository.ListUnknownCollectionEffects(ctx, testOwnerID, workflowID)
	if err != nil || len(remaining) != 1 || remaining[0].ScopeID != second.ScopeID {
		t.Fatalf("remaining uncertain attempts = %#v, error = %v", remaining, err)
	}
}

func TestCompositeImageCollectionFeedbackAndReplayDoNotStartOperations(t *testing.T) {
	t.Parallel()
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%t", restart), func(t *testing.T) {
			ctx := t.Context()
			module, repository, uploads := newCompositeUploadTestModule(t)
			started, err := module.StartUpload(ctx, testOwnerID,
				compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "collection-feedback"))
			if err != nil {
				t.Fatal(err)
			}
			current := waitCompositeUploadTestOperation(t, module, started)
			current = approveCompositeUploadTrackers(t, module, current, []api.TrackerID{"ALPHA"}, "collection-approve")
			if current.Media == nil || current.Descriptions == nil || current.DryRun == nil {
				t.Fatal("collection fixture did not reach the retained description review")
			}
			if err := module.waitForOperationCleanup(ctx, current.Operation.ID); err != nil {
				t.Fatal(err)
			}
			effect := recordUnknownCollectionEffect(ctx, t, repository, current.Workflow.ID, "feedback", module.clock.Now())
			if err := module.publishLegacyRecoveryActions(ctx, testOwnerID, current.Workflow.ID, []api.ReleaseWorkflowEffectRecord{effect}); err != nil {
				t.Fatal(err)
			}
			if restart {
				restarted, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer(),
					WithClock(module.clock), WithProcessEpoch("collection-restarted"))
				if err != nil {
					t.Fatal(err)
				}
				module = restarted
			}
			blocked, err := module.Current(ctx, testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			action := pendingCompositeFeedbackAction(t, blocked, api.RequiredActionReconcileSubmission, "")
			if len(action.Options) != 1 || action.Options[0].Value != api.RequiredActionReconcileNotCompleted ||
				action.Options[0].Label != "I verified no collection was created" || action.EffectScopeID != effect.ScopeID {
				t.Fatalf("collection confirmation authority = %#v", action)
			}
			feedback := api.ReleaseWorkflowUploadFeedback{
				Action: api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: blocked.Workflow.Revision},
				Response: api.ReleaseWorkflowUploadFeedbackResponse{
					Kind:           api.ReleaseWorkflowUploadFeedbackReconciliation,
					Reconciliation: &api.ReleaseWorkflowUploadReconciliation{Selection: api.RequiredActionReconcileNotCompleted},
				},
				IdempotencyKey: "collection-confirm",
			}
			for _, stale := range []api.ReleaseWorkflowUploadActionIdentity{
				{ID: action.ID, WorkflowRevision: blocked.Workflow.Revision - 1},
				{ID: "obsolete-collection-action", WorkflowRevision: blocked.Workflow.Revision},
			} {
				invalid := feedback
				invalid.Action = stale
				if _, err := module.SubmitUploadFeedback(ctx, testOwnerID, blocked.Workflow.ID, invalid); !errors.Is(err, ErrRevisionConflict) {
					t.Fatalf("stale collection feedback = %v", err)
				}
			}
			operations := compositeUploadTestOperationCount(repository, blocked.Workflow.ID)
			builds := uploads.builds
			confirmed, err := module.SubmitUploadFeedback(ctx, testOwnerID, blocked.Workflow.ID, feedback)
			if err != nil {
				t.Fatal(err)
			}
			confirmed, err = module.Current(ctx, testOwnerID, confirmed.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if confirmed.Workflow.Descriptions != nil || confirmed.Workflow.DryRun != nil || len(confirmed.Workflow.RequiredActions) != 0 {
				t.Fatalf("confirmation did not clear description authority: %#v", confirmed.Workflow)
			}
			if restart {
				if blocked.Workflow.Media == nil || blocked.Workflow.Dupes == nil || confirmed.Workflow.Media != nil || confirmed.Workflow.Dupes != nil {
					t.Fatalf("confirmation retained unavailable private authority after restart: %#v", confirmed.Workflow)
				}
			} else if !reflect.DeepEqual(confirmed.Media, blocked.Media) || !reflect.DeepEqual(confirmed.Dupes, blocked.Dupes) {
				t.Fatal("confirmation discarded available private media or duplicate evidence")
			}
			replayed, err := module.SubmitUploadFeedback(ctx, testOwnerID, blocked.Workflow.ID, feedback)
			if err != nil || replayed.Workflow.Revision != confirmed.Workflow.Revision {
				t.Fatalf("confirmation replay = %#v, error = %v", replayed.Workflow, err)
			}
			stale := feedback
			stale.Action.WorkflowRevision = confirmed.Workflow.Revision
			stale.IdempotencyKey = "collection-confirm-stale"
			if _, err := module.SubmitUploadFeedback(ctx, testOwnerID, blocked.Workflow.ID, stale); !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("removed collection action accepted new feedback: %v", err)
			}
			if got := compositeUploadTestOperationCount(repository, blocked.Workflow.ID); got != operations || uploads.builds != builds {
				t.Fatalf("confirmation dispatched work: operations %d -> %d, upload builds %d -> %d", operations, got, builds, uploads.builds)
			}
			state, err := repository.Load(ctx, testOwnerID, blocked.Workflow.ID)
			if err != nil || state.Composite == nil || !state.Composite.FeedbackReceipts[feedback.IdempotencyKey].StopAfterReconciliation {
				t.Fatalf("confirmation did not retain stop-on-replay receipt: error = %v", err)
			}
		})
	}
}

func TestImageCollectionRecoverySurvivesStartupBeforeOrAfterActionPublication(t *testing.T) {
	for _, test := range []struct {
		name      string
		published bool
		failure   string
		owned     bool
	}{
		{name: "crash before action publication"},
		{name: "existing scoped action", published: true},
		{name: "recovered query retry", failure: "query"},
		{name: "recovered publication retry", failure: "save"},
		{
			name:    "owned query retry",
			failure: "query",
			owned:   true,
		},
		{
			name:    "owned publication retry",
			failure: "save",
			owned:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			clock := &mutableClock{now: time.Now().UTC()}
			repo := openActiveInputRecoveryRepository(ctx, t)
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			first := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "collection-before-restart")
			opened, err := first.OpenInput(ctx, testOwnerID, OpenInputRequest{
				Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "synthetic source")},
				IdempotencyKey: "collection-open",
			})
			if err != nil {
				t.Fatal(err)
			}
			authorityCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
			effect := recordUnknownCollectionEffect(authorityCtx, t, persistent, opened.WorkflowID, "restart", clock.Now())
			var originalAction api.RequiredActionID
			if test.published {
				if err := first.publishLegacyRecoveryActions(authorityCtx, testOwnerID, opened.WorkflowID, []api.ReleaseWorkflowEffectRecord{effect}); err != nil {
					t.Fatal(err)
				}
				state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				originalAction = state.Workflow.RequiredActions[0].ID
			}
			restarted := first
			if !test.owned {
				if err := first.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
				clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
				if discarded, _, err := repo.DiscardUnresolvedWorkflowEffectsOnStartup(ctx, clock.Now()); err != nil || discarded != 0 {
					t.Fatalf("startup discarded collection uncertainty: count=%d error=%v", discarded, err)
				}
				restarted = newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "collection-after-restart")
			}
			if test.failure != "" {
				if test.failure == "query" {
					restarted.durability = &failOnceCollectionQuery{DurabilityRepository: persistent}
				} else {
					restarted.repository = &failOnceSaveRepository{Repository: persistent}
				}
				if err := restarted.ResetIdleInputOnStartup(ctx); err == nil {
					t.Fatal("startup accepted the injected collection publication failure")
				}
				state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
				if err != nil || len(state.Workflow.RequiredActions) != 0 {
					t.Fatalf("failed publication changed recovery actions: %#v, error=%v", state.Workflow.RequiredActions, err)
				}
			}
			if err := restarted.ResetIdleInputOnStartup(ctx); err != nil {
				t.Fatal(err)
			}
			state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
			if err != nil || len(state.Workflow.RequiredActions) != 1 || state.Workflow.Status != api.WorkflowStatusBlocked {
				t.Fatalf("startup collection action = %#v, error=%v", state.Workflow, err)
			}
			action := state.Workflow.RequiredActions[0]
			if action.EffectScopeID != effect.ScopeID || action.EffectKind != api.WorkflowExternalEffectImageHosting ||
				action.Kind != api.RequiredActionReconcileSubmission || action.WorkflowRevision != state.Workflow.Revision ||
				(test.published && action.ID != originalAction) {
				t.Fatalf("startup changed collection recovery authority: %#v", action)
			}
			remaining, err := persistent.ListUnknownCollectionEffects(ctx, testOwnerID, opened.WorkflowID)
			if err != nil || len(remaining) != 1 || remaining[0].ScopeID != effect.ScopeID {
				t.Fatalf("startup collection uncertainty = %#v, error=%v", remaining, err)
			}
		})
	}
}

func TestStartupCollectionPublicationRecoversPriorProcessState(t *testing.T) {
	for _, mode := range []string{"expired input", "owned input", "owned recovery retry"} {
		for _, published := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/published=%t", mode, published), func(t *testing.T) {
				ctx := t.Context()
				clock := &mutableClock{now: time.Now().UTC()}
				repo := openActiveInputRecoveryRepository(ctx, t)
				persistent, err := NewPersistentRepository(repo)
				if err != nil {
					t.Fatal(err)
				}
				module := newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "current-process")
				opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
					Input:          api.PrepareInput{SourcePath: writeActiveInputRecoverySource(t, "source.mkv", "synthetic source")},
					IdempotencyKey: "collection-restart-order",
				})
				if err != nil {
					t.Fatal(err)
				}
				authorityCtx := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
				effect := recordUnknownCollectionEffect(authorityCtx, t, persistent, opened.WorkflowID, "restart-order", clock.Now())
				if published {
					if err := module.publishLegacyRecoveryActions(authorityCtx, testOwnerID, opened.WorkflowID, []api.ReleaseWorkflowEffectRecord{effect}); err != nil {
						t.Fatal(err)
					}
				}
				state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				expected := state.Workflow.Revision
				state.Workflow.Revision++
				state.Workflow.Status = api.WorkflowStatusBlocked
				state.ProcessEpoch = "prior-process"
				state.Workflow.RequiredActions = append(state.Workflow.RequiredActions, api.RequiredAction{
					ID:        "obsolete-tracker-approval",
					Kind:      api.RequiredActionApproveTrackers,
					Status:    api.RequiredActionStatusPending,
					Prompt:    "Approve trackers",
					CreatedAt: clock.Now(),
				})
				for i := range state.Workflow.RequiredActions {
					state.Workflow.RequiredActions[i].WorkflowRevision = state.Workflow.Revision
				}
				operationID := api.WorkflowOperationID("interrupted-collection-stage")
				state.Operations[operationID] = api.WorkflowOperationStatus{
					ID:         operationID,
					WorkflowID: opened.WorkflowID,
					Revision:   state.Workflow.Revision,
					Operation:  api.OperationKindImageHosting,
					Status:     api.StageStatusRunning,
					StartedAt:  clock.Now(),
				}
				if err := persistent.Save(authorityCtx, testOwnerID, expected, state); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "expired input":
					if err := module.Shutdown(ctx); err != nil {
						t.Fatal(err)
					}
					clock.now = clock.now.Add(workflowWorkLeaseTTL + time.Second)
					module = newActiveInputRecoveryModule(t, persistent, repo, &hashingActiveInputVerifier{}, clock, "restarted-process")
				case "owned recovery retry":
					module.startupResetPending = true
				}
				if err := module.ResetIdleInputOnStartup(ctx); err != nil {
					t.Fatal(err)
				}
				state, err = persistent.Load(ctx, testOwnerID, opened.WorkflowID)
				if err != nil {
					t.Fatal(err)
				}
				if operation := state.Operations[operationID]; operation.Status != api.StageStatusInterrupted || operation.CompletedAt == nil {
					t.Fatalf("publication skipped restart stage cleanup: %#v", operation)
				}
				if len(state.Workflow.RequiredActions) != 1 || state.Workflow.RequiredActions[0].EffectScopeID != effect.ScopeID ||
					state.Workflow.RequiredActions[0].WorkflowRevision != state.Workflow.Revision || state.ProcessEpoch != module.processEpoch {
					t.Fatalf("publication skipped obsolete approval cleanup or lost reconciliation: %#v", state.Workflow)
				}
			})
		}
	}
}

type failOnceCollectionQuery struct {
	DurabilityRepository
	failed bool
}

func (r *failOnceCollectionQuery) ListUnknownCollectionEffects(ctx context.Context, ownerID string, workflowID api.WorkflowID) ([]api.ReleaseWorkflowEffectRecord, error) {
	if !r.failed {
		r.failed = true
		return nil, errors.New("synthetic collection recovery query failure")
	}
	effects, err := r.DurabilityRepository.ListUnknownCollectionEffects(ctx, ownerID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("query collection recovery effects: %w", err)
	}
	return effects, nil
}

func recordUnknownCollectionEffect(ctx context.Context, t *testing.T, repository DurabilityRepository,
	workflowID api.WorkflowID, attempt string, now time.Time,
) api.ReleaseWorkflowEffectRecord {
	t.Helper()
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          workflowID,
		OperationID:         api.WorkflowOperationID("collection-operation-" + attempt),
		EffectID:            "collection-effect-" + attempt,
		Kind:                string(api.WorkflowExternalEffectImageHosting),
		ScopeID:             api.ImageCollectionEffectScope(attempt),
		SemanticFingerprint: testFingerprint(t, "collection-semantic-"+attempt),
		StartedAt:           now,
		UpdatedAt:           now,
	}
	if _, _, err := repository.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkOperationEffectsUnknown(ctx, testOwnerID, workflowID, effect.OperationID, now); err != nil {
		t.Fatal(err)
	}
	return effect
}

func collectionRecoveryFailure(scope string) api.WorkflowFailure {
	return api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureUnknownOutcome,
			Operation: api.OperationKindImageHosting,
			Message:   "The collection outcome is unknown.",
			Recovery:  api.OperationRecoveryConfirm,
		},
		Resource: scope,
	}
}
