// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"io"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// regression: #809
func TestImageHostingReconciliationRetainsCurrentMedia(t *testing.T) {
	t.Parallel()
	for _, published := range []bool{false, true} {
		name := "effect before snapshot"
		if published {
			name = "published outcome"
		}
		t.Run(name, func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			now := module.clock.Now().UTC()
			action := api.RequiredAction{
				ID: "image-confirmation",
 Kind: api.RequiredActionReconcileSubmission,
				Status: api.RequiredActionStatusPending,
 WorkflowRevision: current.Workflow.Revision + 1,
				EffectKind: api.WorkflowExternalEffectImageHosting,
 EffectScopeID: "imgbox:media-1",
				Prompt: "Verify image hosting.",
 CreatedAt: now,
			}
			state.Workflow.Revision++
			state.Workflow.Status = api.WorkflowStatusBlocked
			state.Workflow.RequiredActions = []api.RequiredAction{action}
			state.Workflow.Media = &api.MediaArtifactSetRef{ID: "media-1", Revision: state.Workflow.Revision}
			snapshot := api.MediaArtifactSet{
				ID: "media-1",
 WorkflowID: state.Workflow.ID,
 Revision: state.Workflow.Revision,
				Release: *state.Workflow.Release,
 ProjectionSet: *state.Workflow.TrackerProjections,
				Status: api.StageStatusPartial,
 CaptureFingerprint: testFingerprint(t, "retained capture"),
				RequirementsFingerprint: testFingerprint(t, "retained requirements"),
 CreatedAt: now,
				Artifacts: []api.MediaArtifact{{
ID: "capture",
 Kind: api.MediaArtifactScreenshot,
 Purpose: api.ScreenshotPurposeFinal,
 Selected: true,
}},
			}
			if published {
				snapshot.RequiredActions = []api.RequiredAction{action}
			}
			state.Media["media-1"] = snapshot
			if err := module.private.Put(testOwnerID, state.Workflow.ID, "media:media-1",
				&retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := repository.Save(t.Context(), testOwnerID, current.Workflow.Revision, state); err != nil {
				t.Fatal(err)
			}
			effect := api.ReleaseWorkflowEffectRecord{
				OwnerID: testOwnerID,
 WorkflowID: state.Workflow.ID,
 OperationID: "hosting",
 EffectID: "hosting",
				Kind: string(action.EffectKind),
 ScopeID: action.EffectScopeID,
 SemanticFingerprint: "synthetic",
				StartedAt: now,
 UpdatedAt: now,
			}
			if _, _, err := repository.BeginEffect(t.Context(), effect); err != nil {
				t.Fatal(err)
			}
			if err := repository.MarkOperationEffectsUnknown(t.Context(), testOwnerID, state.Workflow.ID, effect.OperationID, now); err != nil {
				t.Fatal(err)
			}
			result := executeCommand(t, module, ResolveActionCommand{
				WorkflowID: state.Workflow.ID,
 ExpectedRevision: state.Workflow.Revision,
				Answer: api.RequiredActionAnswer{
ActionID: action.ID,
 WorkflowRevision: state.Workflow.Revision,
					SelectedValues: []string{api.RequiredActionReconcileNotCompleted},
},
			})
			if result.Media == nil || len(result.Media.Artifacts) != 1 || result.Media.Artifacts[0].ID != "capture" ||
				len(result.Workflow.RequiredActions) != 0 {
				t.Fatalf("confirmation lost current capture or retained its action: %#v", result)
			}
			content, err := module.MediaArtifact(t.Context(), testOwnerID, state.Workflow.ID, *result.Workflow.Media, "capture")
			if err != nil {
				t.Fatal(err)
			}
			defer content.Body.Close()
			body, err := io.ReadAll(content.Body)
			if err != nil || len(body) == 0 {
				t.Fatalf("retained capture became unreadable: bytes=%d err=%v", len(body), err)
			}
		})
	}
}

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
