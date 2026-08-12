// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadStrictUnattendedStopsForTrackerApproval(t *testing.T) {
	t.Parallel()

	module, repository, uploads := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-upload")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	if current.Operation == nil || current.Operation.Status != api.StageStatusBlocked ||
		current.UploadResult != nil || current.DryRun != nil || current.Media != nil || uploads.execution != nil {
		t.Fatalf("strict unattended composite result = %#v; execution=%#v", current, uploads.execution)
	}
	action := pendingCompositeTrackerApproval(t, current)
	if !slices.Equal(
		[]string{action.Options[0].Value, action.Options[1].Value},
		[]string{"ALPHA", "BETA"},
	) {
		t.Fatalf("strict unattended tracker approval options = %#v", action.Options)
	}
	state, err := repository.Load(context.Background(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load composite upload state: %v", err)
	}
	if count := compositeUploadTestOperationCount(repository, current.Workflow.ID); count != 1 {
		t.Fatalf("composite upload created %d operations, want one", count)
	}
	if state.Composite == nil || state.Composite.ActiveOperationID != "" ||
		state.Composite.TerminalReason != "feedback_required" || state.Workflow.TrackerApproval != nil {
		t.Fatalf("composite terminal session = %#v", state.Composite)
	}
}

func TestCompositeUploadStrictUnattendedInheritsDefaultTrackers(t *testing.T) {
	t.Parallel()

	module, _, uploads := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-default-trackers")
	request.Trackers.Include = nil

	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(
		t,
		module,
		blocked,
		[]api.TrackerID{"ALPHA", "BETA"},
		"approve-default-trackers",
	)
	if current.Operation == nil || current.Operation.Status != api.StageStatusExecuted || uploads.execution == nil {
		t.Fatalf("default tracker upload operation/execution = %#v/%#v", current.Operation, uploads.execution)
	}
	if current.TrackerApproval == nil ||
		!slices.Equal(current.TrackerApproval.ApprovedTrackerIDs, []api.TrackerID{"ALPHA", "BETA"}) {
		t.Fatalf("default tracker authority = %#v", current.TrackerApproval)
	}
}

func TestCompositeUploadStrictDebugContinuesWithEligibleTrackers(t *testing.T) {
	t.Parallel()

	module, repository, uploads := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "composite-debug")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite debug upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(
		t,
		module,
		blocked,
		[]api.TrackerID{"ALPHA", "BETA"},
		"approve-debug-trackers",
	)
	if current.DryRun == nil || current.Media == nil || current.UploadResult != nil || current.Operation == nil ||
		current.Operation.Status != api.StageStatusCompleted {
		t.Fatalf(
			"composite debug result: revision=%d status=%s dryRun=%t upload=%t failures=%#v",
			current.Workflow.Revision,
			current.Operation.Status,
			current.DryRun != nil,
			current.UploadResult != nil,
			current.Operation.Failures,
		)
	}
	if uploads.execution == nil || uploads.execution.executions != 0 {
		t.Fatalf("debug upload execution plan = %#v", uploads.execution)
	}
	if count := compositeUploadTestOperationCount(repository, current.Workflow.ID); count != 2 {
		t.Fatalf("composite debug created %d operations, want start plus approval resume", count)
	}
}

func TestCompositeUploadConfirmFeedbackResumesWithServerApproval(t *testing.T) {
	t.Parallel()

	module, repository, uploads := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "composite-confirm")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start confirm composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	if blocked.Operation == nil || blocked.Operation.Status != api.StageStatusBlocked || blocked.DryRun != nil || blocked.Media != nil {
		t.Fatalf("confirm composite did not stop at review = %#v", blocked)
	}
	actionIndex := slices.IndexFunc(blocked.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionApproveTrackers && action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 {
		t.Fatalf("confirm composite actions = %#v", blocked.Continuation.RequiredActions)
	}
	action := blocked.Continuation.RequiredActions[actionIndex]
	feedback := api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackTrackerApproval,
			TrackerApproval: &api.ReleaseWorkflowUploadTrackerApproval{
				Confirmed:  true,
				TrackerIDs: []api.TrackerID{"ALPHA"},
			},
		},
		IdempotencyKey: "confirm-upload",
	}
	resumed, err := module.SubmitUploadFeedback(context.Background(), testOwnerID, blocked.Workflow.ID, feedback)
	if err != nil {
		t.Fatalf("submit composite approval: %v", err)
	}
	completed := waitCompositeUploadTestOperation(t, module, resumed)
	if completed.UploadResult == nil || completed.Operation == nil || completed.Operation.Status != api.StageStatusExecuted {
		t.Fatalf("confirmed composite result = %#v", completed)
	}
	if uploads.execution == nil || uploads.execution.executions != 1 ||
		!slices.Equal(uploads.execution.selected, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("confirmed composite executions = %#v", uploads.execution)
	}
	if len(completed.UploadResult.Results) != 1 ||
		completed.UploadResult.Results[0].TrackerID != "ALPHA" ||
		completed.UploadResult.Results[0].Status != api.StageStatusCompleted {
		t.Fatalf("confirmed composite tracker results = %#v", completed.UploadResult.Results)
	}
	replayed, err := module.SubmitUploadFeedback(context.Background(), testOwnerID, blocked.Workflow.ID, feedback)
	if err != nil {
		t.Fatalf("replay composite approval: %v", err)
	}
	if replayed.UploadResult == nil || uploads.execution.executions != 1 {
		t.Fatalf("feedback replay repeated work: result=%#v execution=%#v", replayed, uploads.execution)
	}
	if count := compositeUploadTestOperationCount(repository, completed.Workflow.ID); count != 2 {
		t.Fatalf("confirm composite created %d operations, want start plus resume", count)
	}
}

func TestMergeCompositeProjectionDefaultsPreservesTrackerSpecificValues(t *testing.T) {
	t.Parallel()

	current := api.TrackerProjectionInstructions{
		AdditionalNames: map[string]*string{
			"shared": new("specific"),
		},
		TrackerConfig: api.TrackerConfigOverrides{
			Anon: new(false),
		},
	}
	defaults := api.ReleaseWorkflowUploadTrackerProjection{
		AdditionalNames: map[string]*string{
			"defaultOnly": new("default"),
			"shared":      new("overwritten"),
		},
		Config: api.ReleaseWorkflowUploadTrackerConfig{
			Anon:  new(true),
			Draft: new(false),
		},
	}
	merged := mergeCompositeProjectionDefaults(current, defaults)
	if merged.AdditionalNames["shared"] == nil || *merged.AdditionalNames["shared"] != "specific" ||
		merged.AdditionalNames["defaultOnly"] == nil || *merged.AdditionalNames["defaultOnly"] != "default" ||
		merged.TrackerConfig.Anon == nil || *merged.TrackerConfig.Anon ||
		merged.TrackerConfig.Draft == nil || *merged.TrackerConfig.Draft {
		t.Fatalf("merged projection defaults = %#v", merged)
	}
}

func TestSubmitUploadFeedbackRejectsDeprecatedAuthenticationKinds(t *testing.T) {
	t.Parallel()

	tests := map[string]api.ReleaseWorkflowUploadFeedbackResponse{
		"authentication": {
			Kind: legacyTrackerAuthFeedbackKind,
		},
		"two factor": {
			Kind: legacyTrackerTwoFactorFeedbackKind,
		},
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			module := &Module{}
			_, err := module.SubmitUploadFeedback(
				context.Background(),
				testOwnerID,
				"workflow-legacy-auth",
				api.ReleaseWorkflowUploadFeedback{
					Action: api.ReleaseWorkflowUploadActionIdentity{
						ID:               "action-legacy-auth",
						WorkflowRevision: 2,
					},
					Response:       response,
					IdempotencyKey: "legacy-auth-feedback",
				},
			)
			if !errors.Is(err, ErrInvalidTransition) ||
				!strings.Contains(err.Error(), "outside the upload workflow") ||
				!strings.Contains(err.Error(), "fresh attempt") {
				t.Fatalf("deprecated authentication feedback error = %v", err)
			}
		})
	}
}

func TestCompositeUploadAllTrackersRemoved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		selection  []api.TrackerID
		removed    []api.TrackerID
		wantResult bool
	}{
		{
			name:       "all removed",
			selection:  []api.TrackerID{"ALPHA", "BRAVO"},
			removed:    []api.TrackerID{"BRAVO", "ALPHA"},
			wantResult: true,
		},
		{
			name:      "one remains",
			selection: []api.TrackerID{"ALPHA", "BRAVO"},
			removed:   []api.TrackerID{"ALPHA"},
		},
		{name: "no selection", removed: []api.TrackerID{"ALPHA"}},
		{name: "no removals", selection: []api.TrackerID{"ALPHA"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := CommandResult{}
			if tt.selection != nil {
				current.Selection = &api.TrackerSelection{TrackerIDs: tt.selection}
			}
			session := &compositeUploadSession{RemoveTrackers: tt.removed}
			if got := compositeUploadAllTrackersRemoved(current, session); got != tt.wantResult {
				t.Fatalf("compositeUploadAllTrackersRemoved() = %t, want %t", got, tt.wantResult)
			}
		})
	}
}

func TestCompositeUploadAllAuthBlockedTerminatesNoEligible(t *testing.T) {
	t.Parallel()

	module, repository, uploads := newCompositeUploadTestModule(t)
	module.trackerPreflight = compositeUploadAuthBlockedPreflightBuilder(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-auth-blocked")
	request.Trackers.Include = []api.TrackerID{"ALPHA"}

	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start auth-blocked composite upload: %v", err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	if current.Operation == nil || current.Operation.Status != api.StageStatusFailed ||
		len(current.Operation.Failures) != 1 ||
		current.Operation.Failures[0].Failure.Code != api.OperationFailureNoEligibleTrackers ||
		current.Operation.Failures[0].Failure.Recovery != api.OperationRecoveryAuthenticateTrackers {
		t.Fatalf("auth-blocked composite operation = %#v", current.Operation)
	}
	if current.Workflow.Status != api.WorkflowStatusFailed ||
		len(current.Workflow.RequiredActions) != 0 ||
		len(current.Workflow.Failures) != 1 ||
		current.Workflow.Failures[0].Failure.Code != api.OperationFailureNoEligibleTrackers {
		t.Fatalf("auth-blocked composite workflow = %#v", current.Workflow)
	}
	if current.Dupes != nil || current.Media != nil || current.Descriptions != nil || current.UploadResult != nil || uploads.execution != nil {
		t.Fatalf("auth-blocked composite reached downstream work = %#v", current)
	}
	if current.Selection == nil || !slices.Equal(current.Selection.TrackerIDs, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("auth-blocked composite selection = %#v", current.Selection)
	}
	state, err := repository.Load(context.Background(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load auth-blocked composite state: %v", err)
	}
	if state.Composite == nil || state.Composite.ActiveOperationID != "" ||
		state.Composite.TerminalReason != "no_eligible_trackers" {
		t.Fatalf("auth-blocked composite terminal session = %#v", state.Composite)
	}
}

func TestCompositeUploadStrictExcludesAuthBlockedSibling(t *testing.T) {
	t.Parallel()

	module, _, uploads := newCompositeUploadTestModule(t)
	module.trackerPreflight = compositeUploadAuthBlockedPreflightBuilderFor(t, []api.TrackerID{"BETA"})
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-auth-sibling")

	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start partially auth-blocked composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(
		t,
		module,
		blocked,
		[]api.TrackerID{"ALPHA"},
		"approve-auth-filtered-trackers",
	)
	if current.Operation == nil || current.Operation.Status != api.StageStatusExecuted ||
		current.UploadResult == nil || uploads.execution == nil ||
		!slices.Equal(uploads.execution.selected, []api.TrackerID{"ALPHA"}) {
		t.Fatalf(
			"partially auth-blocked composite result: status=%v upload=%t selected=%v",
			current.Operation,
			current.UploadResult != nil,
			uploads.execution,
		)
	}
	if current.TrackerApproval == nil ||
		!slices.Equal(current.TrackerApproval.ApprovedTrackerIDs, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("partially auth-blocked tracker authority = %#v", current.TrackerApproval)
	}
}

func TestCompositeUploadStrictExcludesDuplicateBlockedSibling(t *testing.T) {
	t.Parallel()

	module, _, uploads := newCompositeUploadTestModule(t)
	module.dupeBuilder = compositeUploadDuplicateBlockedBuilder(module.dupeBuilder, "BETA")
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-dupe-sibling")

	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start duplicate-blocked composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	current := approveCompositeUploadTrackers(
		t,
		module,
		blocked,
		[]api.TrackerID{"ALPHA"},
		"approve-dupe-filtered-trackers",
	)
	if current.Operation == nil || current.Operation.Status != api.StageStatusExecuted ||
		current.UploadResult == nil || uploads.execution == nil ||
		!slices.Equal(uploads.execution.selected, []api.TrackerID{"ALPHA"}) {
		t.Fatalf(
			"duplicate-blocked composite result: status=%v upload=%t execution=%#v",
			current.Operation,
			current.UploadResult != nil,
			uploads.execution,
		)
	}
	if current.TrackerApproval == nil ||
		!slices.Equal(current.TrackerApproval.ApprovedTrackerIDs, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("duplicate-blocked tracker authority = %#v", current.TrackerApproval)
	}
}

func TestCompositeUploadTrackerRemovalUpdateIsIdempotent(t *testing.T) {
	t.Parallel()

	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-tracker-removal", Revision: 4},
		Release:  &api.ReleaseSnapshot{ID: "release-tracker-removal", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA", "BRAVO"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{
			Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:       "projections-tracker-removal",
			Revision: 3,
		},
	}
	session := &compositeUploadSession{
		Intent: api.WorkflowIntent{
			TrackerIDs: []api.TrackerID{"ALPHA", "BRAVO"},
		},
		RemoveTrackers: []api.TrackerID{"ALPHA"},
	}

	trackerIDs, changed := compositeUploadTrackerRemovalUpdate(current, session)
	if !changed || !slices.Equal(trackerIDs, []api.TrackerID{"BRAVO"}) {
		t.Fatalf("first tracker removal update = %#v/%t", trackerIDs, changed)
	}
	session.Intent.TrackerIDs = trackerIDs
	trackerIDs, changed = compositeUploadTrackerRemovalUpdate(current, session)
	if changed || trackerIDs != nil {
		t.Fatalf("repeated tracker removal update = %#v/%t", trackerIDs, changed)
	}

	command, stage := planContinuationCommand(api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-tracker-removal",
		Goal:           api.WorkflowGoalTrackersAssessed,
		Intent:         session.Intent,
	}, current, time.Now())
	projection, ok := command.(ProjectTrackersCommand)
	if !ok || stage != "project-trackers" || !slices.Equal(projection.TrackerIDs, []api.TrackerID{"BRAVO"}) {
		t.Fatalf("tracker removal re-projection: stage=%q command=%#v", stage, command)
	}
}

func compositeUploadTestOperationCount(repository *MemoryRepository, workflowID api.WorkflowID) int {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	count := 0
	for _, operation := range repository.operations {
		if operation.WorkflowID == workflowID {
			count++
		}
	}
	return count
}

func pendingCompositeTrackerApproval(t *testing.T, current CommandResult) api.RequiredAction {
	t.Helper()
	actionIndex := slices.IndexFunc(current.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionApproveTrackers && action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 {
		t.Fatalf("composite tracker approval action = %#v", current.Continuation.RequiredActions)
	}
	return current.Continuation.RequiredActions[actionIndex]
}

func approveCompositeUploadTrackers(
	t *testing.T,
	module *Module,
	current CommandResult,
	trackerIDs []api.TrackerID,
	idempotencyKey string,
) CommandResult {
	t.Helper()
	action := pendingCompositeTrackerApproval(t, current)
	resumed, err := module.SubmitUploadFeedback(context.Background(), testOwnerID, current.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: current.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackTrackerApproval,
			TrackerApproval: &api.ReleaseWorkflowUploadTrackerApproval{
				Confirmed:  true,
				TrackerIDs: trackerIDs,
			},
		},
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		t.Fatalf("submit composite tracker approval: %v", err)
	}
	return waitCompositeUploadTestOperation(t, module, resumed)
}

func compositeUploadTestRequest(
	confirm bool,
	mode api.ReleaseWorkflowUploadMode,
	idempotencyKey string,
) api.CreateReleaseWorkflowUploadRequest {
	return api.CreateReleaseWorkflowUploadRequest{
		Source:     api.ReleaseWorkflowUploadSource{Path: `C:\releases\Example.Release.2026.1080p-GRP`},
		Unattended: &api.ReleaseWorkflowUploadUnattended{Confirm: confirm},
		Execution:  api.ReleaseWorkflowUploadExecution{Mode: mode},
		Trackers: api.ReleaseWorkflowUploadTrackers{
			Include: []api.TrackerID{"ALPHA", "BETA"},
		},
		IdempotencyKey: idempotencyKey,
	}
}

func newCompositeUploadTestModule(
	t *testing.T,
) (*Module, *MemoryRepository, *uploadPlanBuilderFake) {
	t.Helper()
	projections := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		executionMode api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		if len(trackerIDs) == 0 {
			trackerIDs = []api.TrackerID{"ALPHA", "BETA"}
		}
		catalog := testCatalog(t)
		catalog.Trackers = slices.DeleteFunc(catalog.Trackers, func(descriptor api.TrackerCatalogDescriptor) bool {
			return !slices.Contains(trackerIDs, descriptor.TrackerID)
		})
		runtime := testRuntime(t)
		runtime.Trackers = slices.DeleteFunc(runtime.Trackers, func(entry api.TrackerRuntimeEntry) bool {
			return !slices.Contains(trackerIDs, entry.TrackerID)
		})
		projected := make([]api.TrackerReleaseProjection, 0, len(trackerIDs))
		for _, trackerID := range trackerIDs {
			projection := testProjection(t, trackerID, "Example.Release.2026."+string(trackerID)+"-GRP")
			projection.DescriptionGroup = "alpha"
			projected = append(projected, projection)
		}
		return catalog, runtime, api.TrackerSelection{TrackerIDs: trackerIDs}, api.TrackerReleaseProjectionSet{
			InputFingerprint:  testFingerprint(t, "composite-projection-input"),
			PolicyFingerprint: testFingerprint(t, "composite-projection-policy"),
			ExecutionMode:     executionMode,
			Projections:       projected,
			Status:            api.StageStatusReady,
		}, nil
	})
	dupes := dupeAssessmentBuilderFunc(func(
		_ context.Context,
		_ api.DuplicateSubject,
		projectionSet api.TrackerReleaseProjectionSet,
		_ api.TrackerPreflightAssessment,
		now time.Time,
		_ bool,
	) (api.DupeAssessment, any, error) {
		results := make([]api.TrackerDupeAssessment, 0, len(projectionSet.Projections))
		for _, projection := range projectionSet.Projections {
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				return api.DupeAssessment{}, nil, fmt.Errorf("fingerprint composite dupe projection: %w", err)
			}
			results = append(results, api.TrackerDupeAssessment{
				TrackerID:             projection.TrackerID,
				UploadReleaseName:     projection.UploadReleaseName,
				ProjectionFingerprint: fingerprint,
				CriteriaFingerprint:   projection.CriteriaFingerprint,
				Criteria:              projection.DuplicateCriteria,
				Decision:              api.DupeDecisionNoMatch,
				Status:                api.StageStatusCompleted,
				CheckedAt:             now,
				FreshUntil:            now.Add(time.Hour),
			})
		}
		return api.DupeAssessment{
			InputFingerprint: testFingerprint(t, "composite-dupes"),
			Results:          results,
			Status:           api.StageStatusCompleted,
			ExpiresAt:        now.Add(time.Hour),
		}, struct{}{}, nil
	})
	media := mediaArtifactBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseRef,
		projectionSet api.TrackerReleaseProjectionSet,
		_ api.MediaCaptureInstructions,
		_ time.Time,
	) (api.MediaArtifactSet, any, error) {
		requirements, err := mediaRequirementsFingerprint(projectionSet.Projections)
		if err != nil {
			return api.MediaArtifactSet{}, nil, err
		}
		return api.MediaArtifactSet{
			CaptureFingerprint:        testFingerprint(t, "composite-media"),
			RequirementsFingerprint:   requirements,
			ImageRequirementsPrepared: true,
			Status:                    api.StageStatusCompleted,
		}, struct{}{}, nil
	})
	uploadPlans := &uploadPlanBuilderFake{testing: t}
	module, repository := newTestModule(
		t,
		testPreparer(),
		WithTrackerProjectionBuilder(projections),
		WithTrackerPreflightBuilder(compositeUploadReadyPreflightBuilder(t)),
		WithDupeAssessmentBuilder(dupes),
		WithMediaArtifactBuilder(media),
		WithDescriptionBuilder(&descriptionBuilderFake{testing: t}),
		WithUploadPlanBuilder(uploadPlans),
	)
	return module, repository, uploadPlans
}

func compositeUploadReadyPreflightBuilder(t *testing.T) TrackerPreflightBuilder {
	t.Helper()
	base := readyPreflightBuilder(t)
	return trackerPreflightBuilderFunc(func(
		ctx context.Context,
		subject api.UploadSubject,
		catalog api.TrackerCatalogSnapshot,
		runtime api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		assessment, finalized, err := base.Build(ctx, subject, catalog, runtime, initial, now)
		if err != nil {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("build composite upload test preflight: %w", err)
		}
		assessment.ExecutionMode = initial.ExecutionMode
		return assessment, finalized, nil
	})
}

func compositeUploadAuthBlockedPreflightBuilder(t *testing.T) TrackerPreflightBuilder {
	t.Helper()
	return compositeUploadAuthBlockedPreflightBuilderFor(t, []api.TrackerID{"ALPHA", "BETA"})
}

func compositeUploadAuthBlockedPreflightBuilderFor(
	t *testing.T,
	blockedTrackerIDs []api.TrackerID,
) TrackerPreflightBuilder {
	t.Helper()
	base := compositeUploadReadyPreflightBuilder(t)
	return trackerPreflightBuilderFunc(func(
		ctx context.Context,
		subject api.UploadSubject,
		catalog api.TrackerCatalogSnapshot,
		runtime api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		assessment, finalized, err := base.Build(ctx, subject, catalog, runtime, initial, now)
		if err != nil {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("build auth-blocked composite preflight: %w", err)
		}
		for index := range assessment.Results {
			trackerID := assessment.Results[index].TrackerID
			if !slices.Contains(blockedTrackerIDs, trackerID) {
				continue
			}
			failure := api.WorkflowFailure{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureTrackerAuthRequired,
					Operation: api.OperationKindDuplicateCheck,
					Message:   "Tracker authentication is not ready for this attempt.",
					Recovery:  api.OperationRecoveryAuthenticateTrackers,
				},
				TrackerID: trackerID,
			}
			assessment.Results[index].State = api.TrackerPreflightStateRetryable
			assessment.Results[index].AuthReady = false
			assessment.Results[index].RequiredActions = nil
			assessment.Results[index].Failures = []api.WorkflowFailure{failure}
			finalized[index].Readiness = api.ReadinessStatusBlocked
			finalized[index].DupeReady = false
			finalized[index].UploadReady = false
			finalized[index].RequiredActions = nil
			finalized[index].Failures = []api.WorkflowFailure{failure}
		}
		return assessment, finalized, nil
	})
}

func compositeUploadDuplicateBlockedBuilder(
	base DupeAssessmentBuilder,
	blockedTrackerID api.TrackerID,
) DupeAssessmentBuilder {
	return dupeAssessmentBuilderFunc(func(
		ctx context.Context,
		subject api.DuplicateSubject,
		projections api.TrackerReleaseProjectionSet,
		preflight api.TrackerPreflightAssessment,
		now time.Time,
		skipRemote bool,
	) (api.DupeAssessment, any, error) {
		assessment, privateEvidence, err := base.Build(ctx, subject, projections, preflight, now, skipRemote)
		if err != nil {
			return api.DupeAssessment{}, nil, fmt.Errorf("build duplicate-blocked composite assessment: %w", err)
		}
		for index := range assessment.Results {
			if assessment.Results[index].TrackerID != blockedTrackerID {
				continue
			}
			assessment.Results[index].Decision = api.DupeDecisionAccepted
			assessment.Results[index].Matches = []api.DupeMatchProjection{{
				Name:   "Example.Release.2026.1080p-GRP",
				Reason: "same release",
			}}
		}
		return assessment, privateEvidence, nil
	})
}

func waitCompositeUploadTestOperation(
	t *testing.T,
	module *Module,
	started CommandResult,
) CommandResult {
	t.Helper()
	if started.Operation == nil {
		t.Fatalf("composite upload has no operation: %#v", started)
	}
	deadline := time.Now().Add(10 * time.Second)
	operation := *started.Operation
	for !isTerminalProgressStatus(operation.Status) {
		if time.Now().After(deadline) {
			t.Fatalf("composite upload operation timed out: %#v", operation)
		}
		time.Sleep(5 * time.Millisecond)
		var err error
		operation, err = module.Operation(context.Background(), testOwnerID, operation.WorkflowID, operation.ID)
		if err != nil {
			t.Fatalf("poll composite upload operation: %v", err)
		}
	}
	current, err := module.Current(context.Background(), testOwnerID, operation.WorkflowID)
	if err != nil {
		t.Fatalf("load composite upload current: %v", err)
	}
	current.Operation = &operation
	return current
}
