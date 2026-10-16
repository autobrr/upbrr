// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreflightQuestionnaireActionsSharePublishedIdentity(t *testing.T) {
	for _, test := range []struct {
		name           string
		discovered     bool
		projectionOnly bool
		resolved       bool
	}{
		{name: "retained projection action"},
		{name: "new preflight action", discovered: true},
		{name: "finalized-only pending rule", projectionOnly: true},
		{
			name:           "finalized-only resolved rule",
			projectionOnly: true,
			resolved:       true,
		},
		{
			name:           "finalized-only new questionnaire",
			projectionOnly: true,
			discovered:     true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind := api.RequiredActionAnswerQuestionnaire
			status := api.RequiredActionStatusPending
			if test.projectionOnly && !test.discovered {
				kind = api.RequiredActionAuthorizeRules
			}
			if test.resolved {
				status = api.RequiredActionStatusResolved
			}
			template := api.RequiredAction{
				Kind:   kind,
				Status: status,
				Prompt: "Complete required tracker inputs.",
			}
			base := readyPreflightBuilder(t)
			builder := trackerPreflightBuilderFunc(func(
				ctx context.Context,
				subject api.UploadSubject,
				catalog api.TrackerCatalogSnapshot,
				runtime api.TrackerRuntimeSnapshot,
				initial api.TrackerReleaseProjectionSet,
				now time.Time,
			) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
				assessment, finalized, err := base.Build(ctx, subject, catalog, runtime, initial, now)
				if err != nil {
					return assessment, finalized, fmt.Errorf("build questionnaire preflight fixture: %w", err)
				}
				actions := slices.Clone(initial.Projections[0].RequiredActions)
				if test.discovered {
					actions = []api.RequiredAction{template}
				}
				if !test.projectionOnly {
					assessment.Results[0].RequiredActions = slices.Clone(actions)
				}
				if !test.resolved {
					assessment.Results[0].State = api.TrackerPreflightStateActionRequired
					finalized[0].Readiness = api.ReadinessStatusBlocked
					finalized[0].DupeReady = false
					finalized[0].UploadReady = false
				}
				finalized[0].RequiredActions = slices.Clone(actions)
				return assessment, finalized, nil
			})
			module, repository := newTestModule(t, testPreparer(), WithTrackerPreflightBuilder(builder))
			current := executeCommand(t, module, CreateWorkflowCommand{})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")},
			})
			current = executeTestPublication(t, module, trackerContextPublication{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Catalog:          testCatalog(t),
				Runtime:          testRuntime(t),
				Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
			})
			snapshot := testProjectionSet(t)
			if !test.discovered {
				snapshot.Projections[0].RequiredActions = []api.RequiredAction{template}
				if !test.resolved {
					snapshot.Projections[0].Readiness = api.ReadinessStatusBlocked
					snapshot.Projections[0].DupeReady = false
					snapshot.Projections[0].UploadReady = false
				}
			}
			current = executeTestPublication(t, module, projectionSetPublication{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Snapshot:         snapshot,
			})
			initial := *current.Projections
			current = executeCommand(t, module, PreflightTrackersCommand{
				WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision,
			})
			current, err := module.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			action := current.Preflight.Results[0].RequiredActions[0]
			if action.ID == "" || action.TrackerID != "ALPHA" || action.WorkflowRevision != current.Workflow.Revision ||
				action.Status != status || action.ExpiresAt == nil ||
				!action.ExpiresAt.Equal(current.Preflight.Results[0].FreshUntil) {
				t.Fatalf("invalid published questionnaire action: %+v", action)
			}
			if !test.discovered && action.ID != initial.RequiredActions[0].ID {
				t.Fatal("preflight replaced the retained questionnaire action ID")
			}
			for label, actions := range map[string][]api.RequiredAction{
				"workflow":           current.Workflow.RequiredActions,
				"projection set":     current.Projections.RequiredActions,
				"tracker projection": current.Projections.Projections[0].RequiredActions,
				"tracker lane":       current.Continuation.TrackerOutcomes[0].RequiredActions,
			} {
				if label == "tracker lane" && test.resolved {
					if len(actions) != 0 {
						t.Errorf("resolved rule remains actionable in tracker lane: %+v", actions)
					}
					continue
				}
				if len(actions) != 1 || !reflect.DeepEqual(actions[0], action) {
					t.Errorf("%s actions = %+v, want exact published action %+v", label, actions, action)
				}
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(state.Projections[initial.ID], initial) {
				t.Fatal("preflight mutated the original projection snapshot")
			}
		})
	}
}

func TestCompositeQuestionnaireBlocksBeforeUploadAndResumesExactAction(t *testing.T) {
	module, plans := newQuestionnaireCompositeTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "required-questionnaire")
	request.Trackers.Include = []api.TrackerID{"ALPHA"}
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	if blocked.Operation.Status != api.StageStatusBlocked || blocked.Dupes != nil || blocked.Media != nil ||
		blocked.Descriptions != nil || blocked.DryRun != nil || plans.builds != 0 || len(blocked.Continuation.RequiredActions) != 1 {
		t.Fatalf("questionnaire did not stop early: %+v", blocked)
	}
	action := blocked.Continuation.RequiredActions[0]
	if action.Kind != api.RequiredActionAnswerQuestionnaire || action.TrackerID != "ALPHA" ||
		action.WorkflowRevision != blocked.Workflow.Revision {
		t.Fatalf("questionnaire action is not current: %+v", action)
	}
	feedback := api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: action.WorkflowRevision},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackQuestionnaire,
			Questionnaire: &api.ReleaseWorkflowUploadQuestionnaire{
				TrackerID: "BETA", Answers: map[string]*string{"review": new("yes")},
			},
		},
		IdempotencyKey: "answer-required-questionnaire",
	}
	if _, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, blocked.Workflow.ID, feedback); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cross-tracker questionnaire answer error = %v", err)
	}
	feedback.Response.Questionnaire.TrackerID = "ALPHA"
	resumed, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, blocked.Workflow.ID, feedback)
	if err != nil {
		t.Fatal(err)
	}
	ready := waitCompositeUploadTestOperation(t, module, resumed)
	if ready.Projections == nil || ready.Projections.ID == blocked.Projections.ID ||
		ready.Release.Release.Generation != blocked.Release.Release.Generation || !ready.Projections.Projections[0].UploadReady {
		t.Fatalf("answer did not reproject the exact release: %+v", ready)
	}
	stale := feedback
	stale.IdempotencyKey = "stale-questionnaire-action"
	if _, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, blocked.Workflow.ID, stale); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale questionnaire answer error = %v", err)
	}
	completed := approveCompositeUploadTrackers(t, module, ready, []api.TrackerID{"ALPHA"}, "approve-answered-questionnaire")
	if completed.DryRun == nil || !slices.Equal(completed.DryRun.TrackerIDs, []api.TrackerID{"ALPHA"}) || plans.builds != 1 {
		t.Fatalf("answered tracker did not reach upload preparation: %+v", completed)
	}
}

func TestCompositeUnattendedQuestionnaireSkipsOnlyUnansweredTracker(t *testing.T) {
	module, plans := newQuestionnaireCompositeTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "skip-unanswered-questionnaire")
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	ready := waitCompositeUploadTestOperation(t, module, started)
	if ready.Preflight == nil || ready.Dupes == nil || plans.builds != 0 || ready.Operation.Status == api.StageStatusFailed {
		t.Fatalf("ready sibling did not reach approval: %+v", ready)
	}
	completed := approveCompositeUploadTrackers(t, module, ready, []api.TrackerID{"BETA"}, "approve-questionnaire-sibling")
	if completed.Operation.Status != api.StageStatusCompleted || completed.DryRun == nil || plans.builds != 1 ||
		!slices.Equal(completed.DryRun.TrackerIDs, []api.TrackerID{"BETA"}) ||
		!slices.Equal(plans.execution.trackers, []api.TrackerID{"BETA"}) {
		t.Fatalf("unanswered tracker contaminated upload preparation: %+v", completed)
	}
	if completed.Projections.Projections[0].UploadReady || len(completed.Projections.Projections[0].RequiredActions) != 0 {
		t.Fatalf("strict unattended restored unanswered tracker: %+v", completed.Projections.Projections[0])
	}
}

func TestCompositeQuestionnaireRejectsActionAfterReprepare(t *testing.T) {
	module, plans := newQuestionnaireCompositeTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "reprepare-questionnaire")
	request.Trackers.Include = []api.TrackerID{"ALPHA"}
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	if len(blocked.Continuation.RequiredActions) != 1 {
		t.Fatalf("required questionnaire action missing: %+v", blocked.Continuation.RequiredActions)
	}
	action := blocked.Continuation.RequiredActions[0]
	reset := executeCommand(t, module, ResetReleaseCommand{
		WorkflowID:       blocked.Workflow.ID,
		ExpectedRevision: blocked.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: request.Source.Path, Force: true},
	})
	for _, revision := range []api.WorkflowRevision{action.WorkflowRevision, reset.Workflow.Revision} {
		_, err := module.SubmitUploadFeedback(t.Context(), testOwnerID, blocked.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
			Action: api.ReleaseWorkflowUploadActionIdentity{ID: action.ID, WorkflowRevision: revision},
			Response: api.ReleaseWorkflowUploadFeedbackResponse{
				Kind: api.ReleaseWorkflowUploadFeedbackQuestionnaire,
				Questionnaire: &api.ReleaseWorkflowUploadQuestionnaire{
					TrackerID: "ALPHA", Answers: map[string]*string{"review": new("yes")},
				},
			},
			IdempotencyKey: "old-questionnaire-after-reprepare",
		})
		if !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("questionnaire revision %d after reprepare error = %v", revision, err)
		}
	}
	if reset.Workflow.TrackerProjections != nil || plans.builds != 0 {
		t.Fatal("old questionnaire action retained upload authority after reprepare")
	}
}

func newQuestionnaireCompositeTestModule(t *testing.T) (*Module, *uploadPlanBuilderFake) {
	t.Helper()
	module, _, plans := newCompositeUploadTestModule(t)
	baseProjector := module.trackerProjector
	module.trackerProjector = trackerProjectionBuilderFunc(func(
		ctx context.Context,
		release api.ReleaseSnapshot,
		subject api.UploadSubject,
		trackerIDs []api.TrackerID,
		instructions map[api.TrackerID]api.TrackerProjectionInstructions,
		authorizations map[api.TrackerID]api.WorkflowFingerprint,
		mode api.WorkflowExecutionMode,
	) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		catalog, runtime, selection, projections, err := baseProjector.Build(ctx, release, subject, trackerIDs, instructions, authorizations, mode)
		if err != nil {
			return catalog, runtime, selection, projections, fmt.Errorf("build questionnaire projection fixture: %w", err)
		}
		for index := range projections.Projections {
			projection := &projections.Projections[index]
			if projection.TrackerID != "ALPHA" {
				continue
			}
			value := ""
			if answer := instructions["ALPHA"].Questionnaire["review"]; answer != nil {
				value = *answer
			}
			projection.Questionnaire = []api.TrackerQuestionnaireRequirement{{
				Key:      "review",
				Kind:     "text",
				Value:    value,
				Required: true,
			}}
			if value != "" {
				continue
			}
			projection.Readiness = api.ReadinessStatusBlocked
			projection.UploadReady = false
			projection.DupeReady = false
			projection.RequiredActions = []api.RequiredAction{{Kind: api.RequiredActionAnswerQuestionnaire, Prompt: "Complete required tracker inputs."}}
			projections.RequiredActions = append(projections.RequiredActions, projection.RequiredActions...)
		}
		projections.Status = finalizedProjectionStatus(projections.Projections, projections.RequiredActions, projections.Failures)
		return catalog, runtime, selection, projections, nil
	})
	basePreflight := module.trackerPreflight
	module.trackerPreflight = trackerPreflightBuilderFunc(func(
		ctx context.Context,
		subject api.UploadSubject,
		catalog api.TrackerCatalogSnapshot,
		runtime api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		assessment, finalized, err := basePreflight.Build(ctx, subject, catalog, runtime, initial, now)
		if err != nil {
			return assessment, finalized, fmt.Errorf("build questionnaire preflight fixture: %w", err)
		}
		for index := range assessment.Results {
			assessment.Results[index].RequiredActions = slices.Clone(finalized[index].RequiredActions)
			if !finalized[index].DupeReady {
				assessment.Results[index].State = api.TrackerPreflightStateActionRequired
			}
		}
		return assessment, finalized, nil
	})
	return module, plans
}
