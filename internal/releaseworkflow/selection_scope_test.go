// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPersistedAcknowledgementRetainsScopeAfterDemandRefresh(t *testing.T) {
	t.Parallel()

	database, err := db.Open(filepath.Join(t.TempDir(), "selection.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	repository, err := NewPersistentRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	var preparations, projections, defaultProjections atomic.Int32
	preparer := testPreparer()
	basePrepare := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		prepared, err := basePrepare(ctx, input)
		prepared.Release.Generation = api.PreparedGeneration(preparations.Add(1))
		return prepared, err
	}
	demands := &changingSelectionDemand{}
	projector := trackerProjectionBuilderFunc(func(
		_ context.Context,
		release api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		authorizations map[api.TrackerID]api.WorkflowFingerprint,
		executionMode api.WorkflowExecutionMode,
	) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		projections.Add(1)
		if len(trackerIDs) == 0 {
			defaultProjections.Add(1)
			trackerIDs = []api.TrackerID{"AITHER", "BETA"}
		}
		catalog, runtime := testCatalog(t), testRuntime(t)
		catalog.Trackers[0].TrackerID = "AITHER"
		runtime.Trackers[0].TrackerID = "AITHER"
		set := api.TrackerReleaseProjectionSet{
			InputFingerprint:  testFingerprint(t, fmt.Sprint(release.Release.Generation)),
			PolicyFingerprint: testFingerprint(t, "scope-rule-policy"),
			ExecutionMode:     executionMode,
		}
		for _, trackerID := range trackerIDs {
			projection := testProjection(t, trackerID, "Example.Release.2026."+string(trackerID)+"-GRP")
			if trackerID == "AITHER" {
				fingerprint := testFingerprint(t, fmt.Sprintf("modified-release-%d", release.Release.Generation))
				projection.WaivableRuleFingerprint = fingerprint
				projection.PolicyDecisions = []api.TrackerPolicyDecision{{
					Code:        "modified_release",
					Message:     "Modified release",
					Disposition: api.RuleDispositionWaivable,
				}}
				action := api.RequiredAction{Kind: api.RequiredActionAuthorizeRules, Prompt: "Acknowledge modified release?"}
				if authorizations[trackerID] == fingerprint {
					projection.RuleAuthorizationFingerprint = fingerprint
					projection.PolicyDecisions[0].Decision = "authorized"
					action.Status = api.RequiredActionStatusResolved
				} else {
					projection.PolicyDecisions[0].Decision = "authorization_required"
					projection.PolicyDecisions[0].Blocking = true
					projection.Readiness = api.ReadinessStatusBlocked
					projection.DupeReady, projection.UploadReady = false, false
				}
				projection.RequiredActions = []api.RequiredAction{action}
				set.RequiredActions = append(set.RequiredActions, action)
			}
			set.Projections = append(set.Projections, projection)
		}
		set.Status = finalizedProjectionStatus(set.Projections, set.RequiredActions, nil)
		return catalog, runtime, api.TrackerSelection{TrackerIDs: slices.Clone(trackerIDs)}, set, nil
	})
	module, err := New(repository, NewMemoryPrivateResourceStore(), preparer,
		WithClock(&selectionEnrichmentClock{}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(readyPreflightBuilder(t)),
		WithInputReadinessEvaluator(demands),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
	current := executeCommand(t, module, CreateWorkflowCommand{TrackerDecisionMode: TrackerDecisionModeWebUIControls})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")},
		TrackerIDs:       []api.TrackerID{"AITHER"},
	})
	current = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"AITHER"},
	})
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.PreparationInput.MetadataRequirements.Version != "" || state.PreparationDemand.Version == "" {
		t.Fatal("fixture did not exercise separately persisted preparation demand")
	}
	// Change the selected demand explicitly; the original runtime log cannot
	// establish which change first made its retained preparation incompatible.
	demands.changed.Store(true)
	confirmed := true
	answer := api.RequiredActionAnswer{
		ActionID:         current.Workflow.RequiredActions[0].ID,
		WorkflowRevision: current.Workflow.Revision,
		Confirmed:        &confirmed,
	}
	continueOnce := func(answers ...api.RequiredActionAnswer) {
		t.Helper()
		next, continueErr := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
			Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
			IdempotencyKey: "omitted-selection",
			Goal:           api.WorkflowGoalDuplicatesDecided,
			Intent:         api.WorkflowIntent{Interaction: api.InteractionModeInteractive},
			Answers:        answers,
		})
		if continueErr != nil {
			t.Fatalf("continue omitted scope: %v", continueErr)
		}
		if next.Operation != nil && !isTerminalProgressStatus(next.Operation.Status) {
			waitForWorkflowOperation(t, module, next.Workflow.ID, next.Operation.ID, func(status api.WorkflowOperationStatus) bool {
				return isTerminalProgressStatus(status.Status)
			})
		}
		current, continueErr = module.Current(t.Context(), testOwnerID, next.Workflow.ID)
		if continueErr != nil {
			t.Fatal(continueErr)
		}
	}
	continueOnce(answer)
	if current.Projections.Projections[0].RuleAuthorizationFingerprint == "" {
		t.Fatal("initial warning acknowledgement was not accepted")
	}
	continueOnce()
	if preparations.Load() != 2 || current.Selection != nil || current.Projections != nil || current.Dupes != nil || current.Workflow.TrackerApproval != nil {
		t.Fatal("demand refresh did not invalidate generation-bound workflow evidence")
	}
	state, err = repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.TrackerScope, []api.TrackerID{"AITHER"}) || state.PreparationDemand.Version != "selected-demand-v2" {
		t.Fatal("refreshed tracker intent and demand were not persisted together")
	}
	settled := false
	for range 12 {
		previous := current.Workflow.Revision
		continueOnce()
		if current.Workflow.Revision == previous {
			settled = true
			break
		}
	}
	if !settled || preparations.Load() != 2 || defaultProjections.Load() != 0 ||
		current.Selection == nil || !slices.Equal(current.Selection.TrackerIDs, []api.TrackerID{"AITHER"}) {
		t.Fatalf("continuation lost scope or repeated work: settled=%t prepares=%d projections=%d defaults=%d selection=%#v",
			settled, preparations.Load(), projections.Load(), defaultProjections.Load(), current.Selection)
	}
	if current.Projections == nil || len(current.Projections.Projections) != 1 ||
		current.Projections.Projections[0].RuleAuthorizationFingerprint != "" ||
		!slices.ContainsFunc(current.Workflow.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionAuthorizeRules && action.Status == api.RequiredActionStatusPending && action.TrackerID == "AITHER"
		}) {
		t.Fatal("refreshed facts did not require new exact rule authorization")
	}
}

type changingSelectionDemand struct{ changed atomic.Bool }

func (d *changingSelectionDemand) Requirements(_ context.Context, _ []api.TrackerID) (api.MetadataRequirementSet, error) {
	requirements := api.MetadataRequirementSet{Version: "selected-demand-v1"}
	if d.changed.Load() {
		requirements.Version = "selected-demand-v2"
		requirements.Requirements = []api.MetadataRequirement{{
			Scope:       api.MetadataRequirementScopeAny,
			AnyOf:       []api.MetadataRequirementField{"original_title"},
			Disposition: api.RuleDispositionStrict,
		}}
	}
	return requirements, nil
}

func (*changingSelectionDemand) Evaluate(ctx context.Context, subject api.UploadSubject, trackers []api.TrackerID) (api.InputReadinessEvaluation, error) {
	return (selectionDemandEvaluator{}).Evaluate(ctx, subject, trackers)
}

func TestWorkflowTrackerScopePrecedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                string
		requested, retained, selected, want []api.TrackerID
	}{
		{name: "new workflow uses defaults"},
		{
			name:     "older persisted selection",
			selected: []api.TrackerID{"AITHER"},
			want:     []api.TrackerID{"AITHER"},
		},
		{
			name:     "retained scope survives invalidation",
			retained: []api.TrackerID{"AITHER"},
			want:     []api.TrackerID{"AITHER"},
		},
		{
			name:     "accepted scope supersedes old projection",
			retained: []api.TrackerID{"BETA"},
			selected: []api.TrackerID{"AITHER"},
			want:     []api.TrackerID{"BETA"},
		},
		{
			name:      "explicit selection wins",
			requested: []api.TrackerID{"GAMMA"},
			retained:  []api.TrackerID{"BETA"},
			selected:  []api.TrackerID{"AITHER"},
			want:      []api.TrackerID{"GAMMA"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := State{TrackerScope: tc.retained}
			if tc.selected != nil {
				state.Workflow.Selection = &api.TrackerSelectionRef{ID: "selected", Revision: 1}
				state.Selections = map[api.TrackerSelectionID]api.TrackerSelection{"selected": {Revision: 1, TrackerIDs: tc.selected}}
			}
			got := workflowTrackerScope(&state, tc.requested)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("scope = %v, want %v", got, tc.want)
			}
			if len(got) > 0 {
				got[0] = "CHANGED"
				if slices.Contains(tc.requested, "CHANGED") || slices.Contains(tc.retained, "CHANGED") || slices.Contains(tc.selected, "CHANGED") {
					t.Fatal("resolved scope aliases retained state or caller intent")
				}
			}
		})
	}
}
