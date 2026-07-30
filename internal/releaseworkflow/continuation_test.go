// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowContinuationReducesPartialTrackerOutcome(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	current.UploadResult = &api.UploadResult{
		Results: []api.UploadTrackerResult{
			{TrackerID: "ALPHA", Status: api.StageStatusCompleted},
			{TrackerID: "BETA", Status: api.StageStatusFailed},
		},
		Status: api.StageStatusFailed,
	}
	continuation := projectWorkflowContinuation(current)
	if continuation.Lifecycle != api.OperationLifecycleTerminal || continuation.Disposition != api.WorkflowDispositionPartial {
		t.Fatalf("continuation outcome = %s/%s", continuation.Lifecycle, continuation.Disposition)
	}
	if len(continuation.TrackerOutcomes) != 2 ||
		continuation.TrackerOutcomes[0].Disposition != api.WorkflowDispositionSucceeded ||
		continuation.TrackerOutcomes[1].Disposition != api.WorkflowDispositionFailed {
		t.Fatalf("tracker outcomes = %#v", continuation.TrackerOutcomes)
	}
}

func TestWorkflowContinuationFailsOnlyTrackerWithTerminalImageHostFailure(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	current.Media = &api.MediaArtifactSet{
		Status: api.StageStatusCompleted,
		Failures: []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureImageHostUnavailable,
				Operation: api.OperationKindImageHosting,
				Message:   "Required image host failed.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "BETA",
			Resource:  "pixhost",
		}},
	}

	continuation := projectWorkflowContinuation(current)
	if len(continuation.TrackerOutcomes) != 2 ||
		continuation.TrackerOutcomes[0].TrackerID != "ALPHA" ||
		continuation.TrackerOutcomes[0].Disposition == api.WorkflowDispositionFailed {
		t.Fatalf("successful sibling outcome = %#v", continuation.TrackerOutcomes)
	}
	failed := continuation.TrackerOutcomes[1]
	if failed.TrackerID != "BETA" || failed.Disposition != api.WorkflowDispositionFailed ||
		failed.Lifecycle != api.OperationLifecycleTerminal || !failed.Retryable || len(failed.Failures) != 1 {
		t.Fatalf("failed image-host tracker outcome = %#v", failed)
	}
}

func TestTerminalOperationStatusReportsPartialImageHostOutcome(t *testing.T) {
	t.Parallel()

	attemptedAt := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	failure := api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureImageHostUnavailable,
			Operation: api.OperationKindImageHosting,
			Message:   "Required image host failed.",
			Recovery:  api.OperationRecoveryRetry,
		},
		TrackerID: "BETA",
		Resource:  "pixhost",
	}
	command := UploadMediaImagesCommand{}
	mixed := CommandResult{Media: &api.MediaArtifactSet{
		HostAttempts: []api.HostedImageAttempt{
			{
				Host:        "imgbox",
				Status:      api.StageStatusCompleted,
				Results:     []api.MediaArtifact{{ID: "hosted-alpha", Kind: api.MediaArtifactHostedImage}},
				AttemptedAt: attemptedAt,
			},
			{
				Host:        "pixhost",
				Status:      api.StageStatusFailed,
				AttemptedAt: attemptedAt,
			},
		},
		Failures: []api.WorkflowFailure{failure},
	}}
	if got := terminalOperationStatus(command, mixed); got != api.StageStatusPartial {
		t.Fatalf("mixed image-host operation status = %q", got)
	}

	failed := mixed
	failed.Media = &api.MediaArtifactSet{
		HostAttempts: []api.HostedImageAttempt{
			{
				Host:        "imgbox",
				Status:      api.StageStatusCompleted,
				Results:     []api.MediaArtifact{{ID: "prior-hosted-alpha", Kind: api.MediaArtifactHostedImage}},
				AttemptedAt: attemptedAt.Add(-time.Minute),
			},
			{
				Host:        "pixhost",
				Status:      api.StageStatusFailed,
				AttemptedAt: attemptedAt,
			},
		},
		Failures: []api.WorkflowFailure{failure},
	}
	if got := terminalOperationStatus(command, failed); got != api.StageStatusFailed {
		t.Fatalf("failed image-host operation status = %q", got)
	}
}

func TestWorkflowContinuationKeepsRunningRootAndFailedChildDistinct(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	current.Operation = &api.WorkflowOperationStatus{
		ID:         "operation-running",
		WorkflowID: current.Workflow.ID,
		Sequence:   7,
		Command:    "generate_descriptions",
		Phase:      "description",
		Status:     api.StageStatusRunning,
		Message:    "Preparing tracker descriptions.",
		Items: []api.WorkflowOperationItem{
			{
				ID:      "BETA",
				Kind:    "tracker",
				Label:   "BETA",
				Status:  api.StageStatusFailed,
				Message: "Tracker description failed.",
			},
		},
		UpdatedAt: now,
	}
	continuation := projectWorkflowContinuation(current)
	if continuation.Lifecycle != api.OperationLifecycleRunning || continuation.Disposition != api.WorkflowDispositionNone {
		t.Fatalf("root continuation = %s/%s", continuation.Lifecycle, continuation.Disposition)
	}
	if len(continuation.Events) != 2 || continuation.Events[1].Scope != api.WorkflowEventScopeTracker ||
		continuation.Events[1].State != api.StageStatusFailed ||
		continuation.Events[1].Severity != api.WorkflowEventSeverityError {
		t.Fatalf("scoped events = %#v", continuation.Events)
	}
}

func TestOperationSeverityKeepsInfoForOperatorOutcomesOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  api.StageStatus
		scope   api.WorkflowEventScope
		phase   string
		message string
		want    api.WorkflowEventSeverity
	}{
		{
			name:    "workflow completion",
			status:  api.StageStatusCompleted,
			scope:   api.WorkflowEventScopeWorkflow,
			phase:   "projection",
			message: "Operation complete.",
			want:    api.WorkflowEventSeverityInfo,
		},
		{
			name:    "routine tracker completion",
			status:  api.StageStatusCompleted,
			scope:   api.WorkflowEventScopeTracker,
			phase:   "projection",
			message: "Tracker projection complete.",
			want:    api.WorkflowEventSeverityDebug,
		},
		{
			name:    "tracker upload completion",
			status:  api.StageStatusCompleted,
			scope:   api.WorkflowEventScopeTracker,
			phase:   "tracker_upload",
			message: "Tracker upload complete.",
			want:    api.WorkflowEventSeverityInfo,
		},
		{
			name:    "debug bypass outcome",
			status:  api.StageStatusCompleted,
			scope:   api.WorkflowEventScopeTracker,
			phase:   "preflight",
			message: "Debug mode bypassed tracker policy.",
			want:    api.WorkflowEventSeverityInfo,
		},
		{
			name:    "skipped tracker",
			status:  api.StageStatusSkipped,
			scope:   api.WorkflowEventScopeTracker,
			phase:   "projection",
			message: "Tracker is not eligible.",
			want:    api.WorkflowEventSeverityInfo,
		},
		{
			name:    "routine host completion",
			status:  api.StageStatusCompleted,
			scope:   api.WorkflowEventScopeHost,
			phase:   "image_hosting",
			message: "Host upload complete.",
			want:    api.WorkflowEventSeverityDebug,
		},
		{
			name:    "recoverable host failure",
			status:  api.StageStatusFailed,
			scope:   api.WorkflowEventScopeHost,
			phase:   "image_hosting",
			message: "Host upload failed.",
			want:    api.WorkflowEventSeverityWarn,
		},
		{
			name:    "tracker failure",
			status:  api.StageStatusFailed,
			scope:   api.WorkflowEventScopeTracker,
			phase:   "tracker_upload",
			message: "Tracker upload failed.",
			want:    api.WorkflowEventSeverityError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := operationSeverity(test.status, test.scope, test.phase, test.message); got != test.want {
				t.Fatalf("operationSeverity() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestProjectOperationEventChangesOmitsUnchangedTerminalItems(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	previous := api.WorkflowOperationStatus{
		ID:         "operation-dupes",
		WorkflowID: "workflow-events",
		Sequence:   4,
		Command:    "check_duplicates",
		Phase:      "duplicate_check",
		Status:     api.StageStatusRunning,
		Completed:  1,
		Total:      2,
		Message:    "Checking trackers.",
		Items: []api.WorkflowOperationItem{
			{
				ID:        "ALPHA",
				Kind:      "tracker",
				Label:     "ALPHA",
				Phase:     "duplicate_check",
				Status:    api.StageStatusRunning,
				Completed: 0,
				Total:     2,
				Message:   "Searching.",
			},
			{
				ID:        "BETA",
				Kind:      "tracker",
				Label:     "BETA",
				Phase:     "duplicate_check",
				Status:    api.StageStatusSkipped,
				Completed: 1,
				Total:     2,
				Message:   "Policy skipped.",
			},
		},
		UpdatedAt: now,
	}
	current := previous
	current.Sequence++
	current.Completed = 2
	current.Message = "Tracker checked."
	current.UpdatedAt = now.Add(time.Second)
	current.Items = append([]api.WorkflowOperationItem(nil), previous.Items...)
	current.Items[0].Status = api.StageStatusCompleted
	current.Items[0].Completed = 2
	current.Items[0].Message = "No dupes found."

	changes := projectOperationEventChanges(previous, current)
	if len(changes) != 2 || changes[0].Scope != api.WorkflowEventScopeWorkflow ||
		changes[1].ScopeID != "ALPHA" || changes[1].State != api.StageStatusCompleted {
		t.Fatalf("operation event changes = %#v", changes)
	}
}

func TestWorkflowContinuationAdvertisesAndEnforcesDescriptionViability(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	for index := range current.Projections.Projections {
		current.Projections.Projections[index].Artifacts.Description = true
	}
	current.Release = &api.ReleaseSnapshot{}
	current.Preflight = &api.TrackerPreflightAssessment{}
	current.Dupes = &api.DupeAssessment{
		Status: api.StageStatusCompleted,
		Results: []api.TrackerDupeAssessment{
			{
				TrackerID: "ALPHA",
				Status:    api.StageStatusCompleted,
				Decision:  api.DupeDecisionNoMatch,
			},
		},
	}
	current.Media = &api.MediaArtifactSet{Status: api.StageStatusCompleted}
	current.Descriptions = &api.DescriptionSet{
		Status: api.StageStatusFailed,
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "ALPHA", Status: api.StageStatusCompleted},
			{TrackerID: "BETA", Status: api.StageStatusFailed},
		},
	}
	continuation := projectWorkflowContinuation(current)
	upload := findGoalAvailability(t, continuation.AvailableGoals, api.WorkflowGoalUploadReviewed)
	if !upload.Available || !descriptionsHaveViableTracker(current.Descriptions) {
		t.Fatalf("upload availability = %#v descriptions=%#v", upload, current.Descriptions)
	}

	current.Descriptions.TrackerResults[0].Status = api.StageStatusFailed
	continuation = projectWorkflowContinuation(current)
	upload = findGoalAvailability(t, continuation.AvailableGoals, api.WorkflowGoalUploadReviewed)
	if upload.Available || descriptionsHaveViableTracker(current.Descriptions) || upload.ReasonCode != goalReasonDescriptionsRequired {
		t.Fatalf("all-failed upload availability = %#v", upload)
	}
}

func TestWorkflowContinuationExplainsDescriptionMediaReadiness(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	availability := findGoalAvailability(
		t,
		projectWorkflowContinuation(current).AvailableGoals,
		api.WorkflowGoalDescriptionsReady,
	)
	if availability.Available || availability.ReasonCode != goalReasonMediaRequired {
		t.Fatalf("missing-media availability = %#v", availability)
	}

	prompt := "Capture the required menu images."
	current.Media = &api.MediaArtifactSet{
		Status: api.StageStatusBlocked,
		RequiredActions: []api.RequiredAction{{
			Kind:   api.RequiredActionProvideTrackerInput,
			Status: api.RequiredActionStatusPending,
			Prompt: prompt,
		}},
	}
	availability = findGoalAvailability(
		t,
		projectWorkflowContinuation(current).AvailableGoals,
		api.WorkflowGoalDescriptionsReady,
	)
	if availability.Available || availability.Reason != prompt {
		t.Fatalf("blocked-media availability = %#v", availability)
	}

	current.Media = &api.MediaArtifactSet{
		Status: api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{{
			ID:       "screen",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		}},
	}
	availability = findGoalAvailability(
		t,
		projectWorkflowContinuation(current).AvailableGoals,
		api.WorkflowGoalDescriptionsReady,
	)
	if !availability.Available {
		t.Fatalf("normal media without menus should permit description continuation and hosting: %#v", availability)
	}
}

func TestWorkflowContinuationWaitsWhenAllLanesNeedAction(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	action := api.RequiredAction{
		ID:               "action-auth",
		Kind:             legacyTrackerAuthActionKind,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: current.Workflow.Revision,
		TrackerID:        "ALPHA",
		Prompt:           "Authenticate tracker.",
	}
	current.Projections.Projections = current.Projections.Projections[:1]
	current.Projections.Projections[0].RequiredActions = []api.RequiredAction{action}
	current.Workflow.RequiredActions = []api.RequiredAction{action}
	continuation := projectWorkflowContinuation(current)
	if continuation.Lifecycle != api.OperationLifecycleWaiting || continuation.Disposition != api.WorkflowDispositionNeedsAction {
		t.Fatalf("waiting continuation = %s/%s", continuation.Lifecycle, continuation.Disposition)
	}
}

func TestWorkflowContinuationDoesNotAdvertiseFinalUploadApproval(t *testing.T) {
	t.Parallel()

	current := continuationTestCurrent()
	current.DryRun = &api.UploadDryRunResult{
		ID:        "dry-run-exact",
		Revision:  current.Workflow.Revision,
		CreatedAt: time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC),
		Reports: []api.TrackerDryRunReport{{
			TrackerID: "ALPHA",
			Status:    api.StageStatusCompleted,
		}},
	}
	continuation := projectWorkflowContinuation(current)
	if slices.ContainsFunc(continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionApproveUpload //nolint:staticcheck // Verify retained v1 action is absent.
	}) {
		t.Fatalf("obsolete upload approval action = %#v", continuation.RequiredActions)
	}
}

func continuationTestCurrent() CommandResult {
	workflow := api.ReleaseWorkflow{
		ID:       "workflow-continuation",
		Revision: 8,
		Status:   api.WorkflowStatusActive,
	}
	return CommandResult{
		Workflow: workflow,
		Projections: &api.TrackerReleaseProjectionSet{
			Projections: []api.TrackerReleaseProjection{
				{
					TrackerID:   "ALPHA",
					DisplayName: "Alpha",
					Readiness:   api.ReadinessStatusReady,
				},
				{
					TrackerID:   "BETA",
					DisplayName: "Beta",
					Readiness:   api.ReadinessStatusReady,
				},
			},
			Status: api.StageStatusReady,
		},
	}
}

func findGoalAvailability(
	t *testing.T,
	goals []api.GoalAvailability,
	goal api.WorkflowGoal,
) api.GoalAvailability {
	t.Helper()
	for _, candidate := range goals {
		if candidate.Goal == goal {
			return candidate
		}
	}
	t.Fatalf("goal %q not projected", goal)
	return api.GoalAvailability{}
}
