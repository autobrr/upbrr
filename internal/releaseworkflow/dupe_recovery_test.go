// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSettledImageHostingRecoveryRequiresAvailableDuplicateAuthority(t *testing.T) {
	t.Parallel()
	for _, available := range []bool{false, true} {
		name := "unavailable"
		if available {
			name = "current"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			module, repository := newTestModule(t, testPreparer())
			created := executeCommand(t, module, CreateWorkflowCommand{})
			state, err := repository.Load(t.Context(), testOwnerID, created.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			now := module.clock.Now().UTC()
			state.Workflow.Revision++
			state.Workflow.UpdatedAt = now
			state.Workflow.Status = api.WorkflowStatusBlocked
			state.Workflow.Release = &api.ReleaseSnapshotRef{ID: "release-1", Revision: 1}
			state.Workflow.TrackerCatalog = &api.TrackerCatalogSnapshotRef{ID: "catalog-1", Revision: 1}
			state.Workflow.TrackerRuntime = &api.TrackerRuntimeSnapshotRef{ID: "runtime-1", Revision: 1}
			state.Workflow.Selection = &api.TrackerSelectionRef{ID: "selection-1", Revision: 1}
			state.Workflow.ProjectionInstructions = &api.TrackerProjectionInstructionSnapshotRef{ID: "instructions-1", Revision: 1}
			state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1}
			state.Workflow.TrackerPreflight = &api.TrackerPreflightAssessmentRef{ID: "preflight-1", Revision: 1}
			state.Workflow.Dupes = &api.DupeAssessmentRef{ID: "dupes-1", Revision: 1}
			state.Workflow.RequiredActions = []api.RequiredAction{{
				ID:               "settled-image-effect",
				Kind:             api.RequiredActionReconcileSubmission,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: state.Workflow.Revision,
				EffectKind:       api.WorkflowExternalEffectImageHosting,
				EffectScopeID:    "imgbox:media-1",
				Prompt:           "Verify image hosting.",
				CreatedAt:        now,
			}}
			if available {
				if err := module.private.Put(testOwnerID, state.Workflow.ID, "dupe:dupes-1", "current evidence", now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			if err := repository.Save(t.Context(), testOwnerID, created.Workflow.Revision, state); err != nil {
				t.Fatal(err)
			}
			// The effect ledger is already settled, but the retained feedback action remains.
			settled, err := module.settleRecoveryActions(t.Context(), testOwnerID, state.Workflow.ID)
			if err != nil || !settled {
				t.Fatalf("settle image-host recovery = %t, error=%v", settled, err)
			}
			state, err = repository.Load(t.Context(), testOwnerID, state.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (state.Workflow.Dupes != nil) != available || (state.Workflow.TrackerProjections != nil) != available ||
				(state.Workflow.TrackerPreflight != nil) != available {
				t.Fatalf("settled workflow retains wrong duplicate authority: %#v", state.Workflow)
			}
			if state.Workflow.Release == nil || state.Workflow.Selection == nil || state.Workflow.ProjectionInstructions == nil ||
				len(state.Workflow.RequiredActions) != 0 || state.Workflow.Status != api.WorkflowStatusActive {
				t.Fatalf("settlement discarded intent or retained feedback: %#v", state.Workflow)
			}
		})
	}
}

func TestPendingReconciliationPreservesAuthorityUntilEveryEffectIsKnown(t *testing.T) {
	t.Parallel()
	module, _ := newTestModule(t, testPreparer())
	workflow := api.ReleaseWorkflow{
		ID:                 "pending-effects",
		Dupes:              &api.DupeAssessmentRef{ID: "dupes-1", Revision: 1},
		TrackerProjections: &api.TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1},
		RequiredActions:    []api.RequiredAction{{Kind: api.RequiredActionReconcileSubmission, Status: api.RequiredActionStatusPending}},
	}
	if err := module.invalidateUnavailablePrivateAuthority(testOwnerID, &workflow, module.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if workflow.Dupes == nil || workflow.TrackerProjections == nil {
		t.Fatal("uncertain external effects lost their retained authority")
	}
}
