// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinueReplacesStoredRemoteResultsWithWaiver(t *testing.T) {
	t.Parallel()
	for _, priorDecision := range []api.DupeDecision{api.DupeDecisionNoMatch, api.DupeDecisionAccepted, api.DupeDecisionPending} {
		t.Run(string(priorDecision), func(t *testing.T) {
			t.Parallel()
			module, repository, _ := newCompositeUploadTestModule(t)
			base := readyDupeBuilder(t)
			var modes []bool
			module.dupeBuilder = dupeAssessmentBuilderFunc(func(ctx context.Context, subject api.DuplicateSubject,
				projections api.TrackerReleaseProjectionSet, preflight api.TrackerPreflightAssessment, now time.Time, skipRemote bool,
			) (api.DupeAssessment, any, error) {
				modes = append(modes, skipRemote)
				assessment, evidence, err := base(ctx, subject, projections, preflight, now, skipRemote)
				for index := range assessment.Results {
					result := &assessment.Results[index]
					if result.TrackerID == "BETA" {
						result.Decision = api.DupeDecisionAccepted
						result.Matches = []api.DupeMatchProjection{{
							Name:     "Example.Release.2026.1080p-GRP",
							Reason:   "in_client",
							Relation: api.DupeRelationExactDuplicate,
						}}
						continue
					}
					if skipRemote {
						result.Decision, result.Status = api.DupeDecisionSkipped, api.StageStatusSkipped
						continue
					}
					result.Decision = priorDecision
					if priorDecision != api.DupeDecisionNoMatch {
						result.Matches = []api.DupeMatchProjection{{
							Name: "Example.Release.2026.1080p-GRP", Relation: api.DupeRelationExactDuplicate,
						}}
					}
					if priorDecision == api.DupeDecisionPending {
						result.RequiresStaffToken = true
						RequireStaffTokenReview(result)
					}
				}
				return assessment, evidence, err
			})
			current := executeCommand(t, module, CreateWorkflowCommand{TrackerDecisionMode: TrackerDecisionModePostDupeGate})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: "Example.Release.2026.1080p-GRP.mkv"},
			})
			current = executeCommand(t, module, EvaluateInputReadinessCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
			})
			current = executeCommand(t, module, ProjectTrackersCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
			})
			current = executeCommand(t, module, PreflightTrackersCommand{
				WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision,
			})
			current = executeCommand(t, module, CheckDuplicatesCommand{
				WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision,
			})
			current, err := module.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if priorDecision == api.DupeDecisionNoMatch {
				action := pendingCompositeTrackerApproval(t, current)
				current = executeCommand(t, module, ApproveTrackersCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					Approval: api.TrackerApproval{
						ActionID:         action.ID,
						Dupes:            *current.Workflow.Dupes,
						InputFingerprint: current.Dupes.InputFingerprint,
						TrackerIDs:       []api.TrackerID{"ALPHA"},
					},
				})
				current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			priorID := current.Dupes.ID
			request := api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
				IdempotencyKey: "replace-stored-remote-result",
				Goal:           api.WorkflowGoalDuplicatesDecided,
				Intent: api.WorkflowIntent{
					TrackerIDs:           []api.TrackerID{"ALPHA", "BETA"},
					SkipRemoteDuplicates: true,
					DuplicateDecisions:   map[api.TrackerID]api.DupeDecision{"ALPHA": api.DupeDecisionAccepted},
				},
			}
			started, err := module.Continue(t.Context(), testOwnerID, request)
			if err != nil || started.Operation == nil {
				t.Fatalf("continue changed remote search mode: operation=%#v err=%v", started.Operation, err)
			}
			waitForWorkflowOperation(t, module, current.Workflow.ID, started.Operation.ID, func(status api.WorkflowOperationStatus) bool {
				return isTerminalProgressStatus(status.Status)
			})
			refreshed, err := module.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if refreshed.Dupes == nil || refreshed.Dupes.ID == priorID || !refreshed.Dupes.SkipRemote ||
				refreshed.TrackerApproval != nil || !slices.Equal(modes, []bool{false, true}) {
				t.Fatalf("stored remote authority was retained: dupes=%#v approval=%#v modes=%v", refreshed.Dupes, refreshed.TrackerApproval, modes)
			}
			alpha, beta := refreshed.Dupes.Results[0], refreshed.Dupes.Results[1]
			if alpha.TrackerID != "ALPHA" || alpha.Decision != api.DupeDecisionSkipped || len(alpha.Matches) != 0 || alpha.RequiresStaffToken ||
				beta.TrackerID != "BETA" || beta.Decision != api.DupeDecisionAccepted || len(beta.Matches) != 1 || beta.Matches[0].Reason != "in_client" {
				t.Fatalf("waiver or in-client block lost: %#v", refreshed.Dupes.Results)
			}
			action := pendingCompositeTrackerApproval(t, refreshed)
			if len(action.Options) != 1 || action.Options[0].Value != "ALPHA" {
				t.Fatalf("waiver widened approval candidates: %#v", action.Options)
			}
			stored, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil || !stored.Dupes[refreshed.Dupes.ID].SkipRemote {
				t.Fatalf("waiver mode was not stored: %v", err)
			}
		})
	}
}

func TestContinuationRechecksStoredDuplicateSearchMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		storedSkip bool
		skipRemote bool
		decision   api.DupeDecision
	}{
		{
			name:       "stored clear",
			skipRemote: true,
			decision:   api.DupeDecisionNoMatch,
		},
		{
			name:       "stored remote block",
			skipRemote: true,
			decision:   api.DupeDecisionAccepted,
		},
		{
			name:       "stored review",
			skipRemote: true,
			decision:   api.DupeDecisionPending,
		},
		{
			name:       "resume remote checks",
			storedSkip: true,
			decision:   api.DupeDecisionSkipped,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
			current := readyContinuationPlannerResult(t, now)
			current.Dupes.SkipRemote = tc.storedSkip
			current.Dupes.CheckOrdinal = 2
			current.Dupes.Results[0].Decision = tc.decision
			request := api.ContinueReleaseWorkflowRequest{
				IdempotencyKey: "changed-remote-search",
				Goal:           api.WorkflowGoalDuplicatesDecided,
				Intent: api.WorkflowIntent{
					TrackerIDs:           []api.TrackerID{"ALPHA"},
					SkipRemoteDuplicates: tc.skipRemote,
				},
			}
			if continuationGoalReached(current, request) {
				t.Fatal("stored search mode satisfied changed duplicate intent")
			}
			command, stage := planContinuationCommand(request, current, now)
			check, ok := command.(CheckDuplicatesCommand)
			if !ok || stage != "check-duplicates" || check.SkipRemote != tc.skipRemote || check.CheckOrdinal != 1 {
				t.Fatalf("changed search mode plan: stage=%q command=%#v", stage, command)
			}
			action := api.RequiredAction{
				Kind:      api.RequiredActionReviewDuplicates,
				Status:    api.RequiredActionStatusPending,
				TrackerID: "ALPHA",
			}
			current.Continuation.RequiredActions = []api.RequiredAction{action}
			session := &compositeUploadSession{Intent: request.Intent, Confirm: true}
			if blocked := compositeUploadPendingAction(current, session); blocked != nil {
				t.Fatalf("stored remote review blocked changed search mode: %#v", blocked)
			}
			current.DryRun = &api.UploadDryRunResult{Revision: 9, Status: api.StageStatusCompleted}
			session.Goal = api.WorkflowGoalDryRun
			if compositeUploadGoalReached(current, session, 8) {
				t.Fatal("stored dry run satisfied changed duplicate intent")
			}
		})
	}
}

func TestContinuationWaivedSearchIgnoresRetainedRemoteDecisions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Dupes.SkipRemote = true
	current.Dupes.Results[0].Decision = api.DupeDecisionSkipped
	current.Dupes.Results[0].Status = api.StageStatusSkipped
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "waived-search-retained-decision",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent: api.WorkflowIntent{
			TrackerIDs:           []api.TrackerID{"ALPHA"},
			SkipRemoteDuplicates: true,
			DuplicateDecisions:   map[api.TrackerID]api.DupeDecision{"ALPHA": api.DupeDecisionAccepted},
		},
	}
	if !continuationGoalReached(current, request) {
		t.Fatal("retained remote decision prevented the explicitly waived result")
	}
	if command, stage := planContinuationCommand(request, current, now); command != nil || stage != "" {
		t.Fatalf("retained remote decision changed waived assessment: stage=%q command=%#v", stage, command)
	}
}
