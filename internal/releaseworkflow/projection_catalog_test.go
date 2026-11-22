// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCatalogRefreshPreservesExternalOutcomeAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		result *api.UploadResultRef
		effect api.WorkflowExternalEffectKind
	}{
		{name: "retained upload result", result: &api.UploadResultRef{ID: "upload", Revision: 1}},
		{
			name:   "uncertain tracker submission",
			result: &api.UploadResultRef{ID: "upload", Revision: 1},
			effect: api.WorkflowExternalEffectTrackerSubmission,
		},
		{name: "uncertain image hosting", effect: api.WorkflowExternalEffectImageHosting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			module := Module{trackerProjector: upgradedCatalogProjector{stale: "old-catalog"}}
			current := CommandResult{
				Workflow:    api.ReleaseWorkflow{Status: api.WorkflowStatusBlocked, UploadResult: tc.result},
				Release:     &api.ReleaseSnapshot{},
				Catalog:     &api.TrackerCatalogSnapshot{ID: "old-catalog"},
				Projections: &api.TrackerReleaseProjectionSet{ID: "retained-projections"},
			}
			if tc.effect != "" {
				current.Workflow.RequiredActions = []api.RequiredAction{{
					Kind:       api.RequiredActionReconcileSubmission,
					Status:     api.RequiredActionStatusPending,
					EffectKind: tc.effect,
				}}
			}
			command, err := module.planCatalogRefresh(api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalUploaded}, current, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if command != nil {
				t.Fatalf("catalog refresh would replace external outcome authority: %#v", command)
			}
		})
	}
}

func TestCatalogRefreshRetainsOmittedProjectionInstructions(t *testing.T) {
	t.Parallel()
	saved := map[api.TrackerID]api.TrackerProjectionInstructions{
		"ALPHA": {TrackerConfig: api.TrackerConfigOverrides{Anon: new(true)}, ScreenshotCount: new(3)},
	}
	for _, tc := range []struct {
		name            string
		requested, want map[api.TrackerID]api.TrackerProjectionInstructions
	}{
		{name: "omitted", want: saved},
		{
			name:      "explicit clear",
			requested: map[api.TrackerID]api.TrackerProjectionInstructions{},
			want:      map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
		{
			name:      "explicit false",
			requested: map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {TrackerConfig: api.TrackerConfigOverrides{Anon: new(false)}}},
			want:      map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {TrackerConfig: api.TrackerConfigOverrides{Anon: new(false)}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			module := Module{trackerProjector: upgradedCatalogProjector{stale: "old-catalog"}}
			current := CommandResult{
				Release:                &api.ReleaseSnapshot{},
				Catalog:                &api.TrackerCatalogSnapshot{ID: "old-catalog"},
				Projections:            &api.TrackerReleaseProjectionSet{},
				ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: saved},
			}
			command, err := module.planCatalogRefresh(api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalUploaded, Intent: api.WorkflowIntent{ProjectionInstructions: tc.requested}}, current, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			projection, ok := command.(ProjectTrackersCommand)
			if !ok || !reflect.DeepEqual(projection.Instructions, tc.want) {
				t.Fatalf("refreshed instructions = %#v, want %#v", command, tc.want)
			}
		})
	}
}

func TestContinueHydratesBeforeRestartedCatalogRefresh(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted preparation", true: "supplied preparation"}[supplied], func(t *testing.T) {
			first, repository, _ := newCompositeUploadTestModule(t)
			demand := api.MetadataRequirementSet{Version: "catalog-restart-v1", Requirements: []api.MetadataRequirement{{
				Scope:       api.MetadataRequirementScopeAny,
				AnyOf:       []api.MetadataRequirementField{"original_title"},
				Disposition: api.RuleDispositionStrict,
			}}}
			first.inputReadiness = compositeUploadDemandEvaluator{requirements: demand}
			input := api.PrepareInput{
				SourcePath: filepath.Join(t.TempDir(), "Example.mkv"),
				Force:      true,
				Controls:   api.PreparationControls{ConfirmBDMVRescan: true, ForceRecheck: new(true)},
			}
			current := executeCommand(t, first, CreateWorkflowCommand{WorkflowID: "catalog-restart", Instructions: api.ReleaseFactInstructions{SourceLookup: "saved effective lookup"}})
			current = executeCommand(t, first, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            input,
				TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
			})
			current = executeCommand(t, first, ProjectTrackersCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
				Instructions:     map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {TrackerConfig: api.TrackerConfigOverrides{Anon: new(true)}}},
			})
			current, err := first.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if continuationPreparationSatisfied(current.Release, state.PreparationInput) {
				t.Fatal("fixture lacks effective-fact transformation")
			}
			var hydrations atomic.Int32
			hydrated := make(chan api.PrepareInput, 1)
			base := testPreparer()
			preparer := ReleasePreparerFunc{
				PrepareFunc: func(_ context.Context, prepared api.PrepareInput) (api.PrepareResult, error) {
					hydrated <- prepared
					hydrations.Add(1)
					return api.PrepareResult{Release: current.Release.Release}, nil
				},
				DisplayFunc:   base.ResolveDisplay,
				DuplicateFunc: base.ResolveDuplicateSubject,
				SubjectFunc: func(ctx context.Context, subject api.UploadSubjectInput) (api.UploadSubject, error) {
					if hydrations.Load() == 0 {
						return api.UploadSubject{}, errors.New("prepared envelope not restored")
					}
					return base.ResolveUploadSubject(ctx, subject)
				},
			}
			restarted, err := New(repository, first.private, preparer, WithIDGenerator(&sequenceIDGenerator{next: 100}), WithTrackerProjectionBuilder(upgradedCatalogProjector{TrackerProjectionBuilder: first.trackerProjector, stale: current.Catalog.ID}))
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := restarted.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			request := api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: resumed.Workflow.ID, ExpectedRevision: resumed.Workflow.Revision},
				Goal:           api.WorkflowGoalDryRun,
				IdempotencyKey: "restart-catalog",
			}
			if supplied {
				request.Intent.Preparation = &input
			}
			continued, err := restarted.Continue(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatal(err)
			}
			result := waitCompositeUploadTestOperation(t, restarted, continued)
			if result.Operation.Status != api.StageStatusCompleted || result.Projections == nil || result.Projections.ID == current.Projections.ID {
				t.Fatalf("restart refresh failed: %+v", result.Operation)
			}
			if result.Release.Release.Generation != current.Release.Release.Generation || !reflect.DeepEqual(result.ProjectionInstructions.Instructions, current.ProjectionInstructions.Instructions) {
				t.Fatal("catalog refresh changed generation or lost saved upload choices")
			}
			if hydrations.Load() != 1 {
				t.Fatalf("hydration calls = %d, want 1", hydrations.Load())
			}
			prepared := <-hydrated
			if !prepared.RequirePrepared || prepared.Force || prepared.ExternalFreshness != api.ExternalFreshnessReuse || prepared.Controls.ConfirmBDMVRescan || prepared.Controls.ForceRecheck != nil || !reflect.DeepEqual(prepared.MetadataRequirements, state.PreparationDemand) {
				t.Fatalf("unsafe hydration input: %+v", prepared)
			}
		})
	}
}

func TestContinueRefreshesCatalogBeforeUnansweredQuestionnaire(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current catalog remains blocked", true: "stale catalog refreshes"}[stale], func(t *testing.T) {
			module, _ := newQuestionnaireCompositeTestModule(t)
			input := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "catalog-questionnaire")
			input.Trackers.Include = []api.TrackerID{"ALPHA"}
			started, err := module.StartUpload(t.Context(), testOwnerID, input)
			if err != nil {
				t.Fatal(err)
			}
			blocked := waitCompositeUploadTestOperation(t, module, started)
			pendingCompositeFeedbackAction(t, blocked, api.RequiredActionAnswerQuestionnaire, "ALPHA")
			// Give the continuation a distinct timestamp from the completed fixture operation.
			module.clock = fixedClock{now: module.clock.Now().Add(time.Second)}
			if stale {
				module.trackerProjector = upgradedCatalogProjector{TrackerProjectionBuilder: module.trackerProjector, stale: blocked.Catalog.ID}
			}
			continued, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: blocked.Workflow.ID, ExpectedRevision: blocked.Workflow.Revision},
				Goal:           api.WorkflowGoalDryRun,
				IdempotencyKey: "refresh-questionnaire-catalog",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !stale {
				if continued.Projections.ID != blocked.Projections.ID || continued.Workflow.Revision != blocked.Workflow.Revision {
					t.Fatal("current unanswered questionnaire was bypassed")
				}
				return
			}
			result := waitCompositeUploadTestOperation(t, module, continued)
			if result.Projections.ID == blocked.Projections.ID {
				t.Fatal("stale catalog remained blocked on obsolete questionnaire")
			}
			if result.Release.Release.Generation != blocked.Release.Release.Generation {
				t.Fatal("catalog refresh replaced compatible source facts")
			}
		})
	}
}
