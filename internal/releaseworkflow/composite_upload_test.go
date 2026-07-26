// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadStrictUnattendedCompletesBehindOneOperation(t *testing.T) {
	t.Parallel()

	module, repository, uploads := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-upload")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	if current.UploadResult == nil || current.DryRun == nil || current.Operation == nil {
		t.Fatalf("composite upload result = %#v", current)
	}
	if current.Operation.Status != api.StageStatusExecuted || uploads.execution == nil || uploads.execution.executions != 1 {
		t.Fatalf("composite upload operation/execution = %#v/%#v", current.Operation, uploads.execution)
	}
	state, err := repository.Load(context.Background(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatalf("load composite upload state: %v", err)
	}
	if count := compositeUploadTestOperationCount(repository, current.Workflow.ID); count != 1 {
		t.Fatalf("composite upload created %d operations, want one", count)
	}
	if state.Composite == nil || state.Composite.ActiveOperationID != "" || state.Composite.TerminalReason != "goal_reached" {
		t.Fatalf("composite terminal session = %#v", state.Composite)
	}
}

func TestCompositeUploadDebugStopsAtExactDryRun(t *testing.T) {
	t.Parallel()

	module, repository, uploads := newCompositeUploadTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "composite-debug")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite debug upload: %v", err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	if current.DryRun == nil || current.UploadResult != nil || current.Operation == nil ||
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
		t.Fatalf("debug upload executed tracker submission = %#v", uploads.execution)
	}
	if count := compositeUploadTestOperationCount(repository, current.Workflow.ID); count != 1 {
		t.Fatalf("composite debug created %d operations, want one", count)
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
	if blocked.Operation == nil || blocked.Operation.Status != api.StageStatusBlocked || blocked.DryRun == nil {
		t.Fatalf("confirm composite did not stop at review = %#v", blocked)
	}
	actionIndex := slices.IndexFunc(blocked.Workflow.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionApproveUpload && action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 {
		t.Fatalf("confirm composite actions = %#v", blocked.Workflow.RequiredActions)
	}
	action := blocked.Workflow.RequiredActions[actionIndex]
	feedback := api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackUploadApproval,
			UploadApproval: &api.ReleaseWorkflowUploadApproval{
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
	if len(completed.UploadResult.Results) != 2 ||
		completed.UploadResult.Results[0].TrackerID != "ALPHA" ||
		completed.UploadResult.Results[0].Status != api.StageStatusCompleted ||
		completed.UploadResult.Results[1].TrackerID != "BETA" ||
		completed.UploadResult.Results[1].Status != api.StageStatusSkipped {
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

func TestCompositeAuthenticationFeedbackTransitionsToTwoFactorWithoutPersistingCode(t *testing.T) {
	t.Parallel()

	authenticator := &compositeUploadAuthTestFake{
		validate: api.TrackerAuthStatus{
			TrackerID: "ALPHA",
			State:     "login_required",
		},
		login: api.TrackerAuthStatus{
			TrackerID:   "ALPHA",
			State:       "login_required",
			Needs2FA:    true,
			ChallengeID: "challenge-synthetic",
		},
	}
	module := &Module{uploadAuthenticator: authenticator}
	secret, err := module.executeCompositeSecretFeedback(
		context.Background(),
		api.RequiredAction{TrackerID: "ALPHA"},
		api.ReleaseWorkflowUploadFeedback{
			Response: api.ReleaseWorkflowUploadFeedbackResponse{
				Kind: api.ReleaseWorkflowUploadFeedbackTrackerAuthentication,
				TrackerAuthentication: &api.ReleaseWorkflowUploadTrackerAuthentication{
					TrackerID: "ALPHA",
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("execute authentication feedback: %v", err)
	}
	if !secret.AuthenticationNeedsTwoFactor ||
		secret.AuthenticationChallengeID != "challenge-synthetic" ||
		authenticator.loginCalls != 1 {
		t.Fatalf("secret authentication result = %#v calls=%d", secret, authenticator.loginCalls)
	}

	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:       "workflow-auth",
			Revision: 2,
			Status:   api.WorkflowStatusBlocked,
			RequiredActions: []api.RequiredAction{{
				ID:               "action-auth",
				Kind:             api.RequiredActionAuthenticateTracker,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: 2,
				TrackerID:        "ALPHA",
			}},
		},
		Composite: &compositeUploadSession{
			FeedbackReceipts: make(map[string]compositeUploadFeedbackReceipt),
		},
	}
	_, err = module.applyCompositeUploadFeedback(
		context.Background(),
		testOwnerID,
		&state,
		3,
		now,
		applyCompositeUploadFeedbackCommand{
			WorkflowID:       state.Workflow.ID,
			ExpectedRevision: 2,
			ActionID:         "action-auth",
			IdempotencyKey:   "auth-to-two-factor",
			Response: compositeUploadFeedbackResponse{
				Kind:                         api.ReleaseWorkflowUploadFeedbackTrackerAuthentication,
				TrackerID:                    "ALPHA",
				AuthenticationNeedsTwoFactor: true,
				AuthenticationChallengeID:    "challenge-synthetic",
			},
		},
	)
	if err != nil {
		t.Fatalf("apply authentication feedback: %v", err)
	}
	action := state.Workflow.RequiredActions[0]
	if action.Kind != api.RequiredActionProvideTwoFactor ||
		action.WorkflowRevision != 3 ||
		len(action.Options) != 1 ||
		action.Options[0].Value != "challenge-synthetic" {
		t.Fatalf("two-factor action = %#v", action)
	}
}

func TestCompositeAuthenticationFeedbackSkipsRemoteValidationFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		validate       api.TrackerAuthStatus
		login          api.TrackerAuthStatus
		wantLoginCalls int
		wantSkip       bool
	}{
		{
			name: "validation unavailable",
			validate: api.TrackerAuthStatus{
				TrackerID: "ALPHA",
				State:     "configured",
				LastError: "remote validation unavailable",
			},
			wantSkip: true,
		},
		{
			name: "login validation becomes unavailable",
			validate: api.TrackerAuthStatus{
				TrackerID: "ALPHA",
				State:     "login_required",
			},
			login: api.TrackerAuthStatus{
				TrackerID: "ALPHA",
				State:     "configured",
				LastError: "remote validation unavailable",
			},
			wantLoginCalls: 1,
			wantSkip:       true,
		},
		{
			name: "authentication remains required",
			validate: api.TrackerAuthStatus{
				TrackerID: "ALPHA",
				State:     "login_required",
			},
			login: api.TrackerAuthStatus{
				TrackerID: "ALPHA",
				State:     "login_required",
				LastError: "cookies unavailable",
			},
			wantLoginCalls: 1,
			wantSkip:       true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authenticator := &compositeUploadAuthTestFake{
				validate: test.validate,
				login:    test.login,
			}
			module := &Module{uploadAuthenticator: authenticator}
			secret, err := module.executeCompositeSecretFeedback(
				context.Background(),
				api.RequiredAction{TrackerID: "ALPHA"},
				api.ReleaseWorkflowUploadFeedback{
					Response: api.ReleaseWorkflowUploadFeedbackResponse{
						Kind: api.ReleaseWorkflowUploadFeedbackTrackerAuthentication,
						TrackerAuthentication: &api.ReleaseWorkflowUploadTrackerAuthentication{
							TrackerID: "ALPHA",
						},
					},
				},
			)
			if err != nil {
				t.Fatalf("execute unavailable authentication feedback: %v", err)
			}
			if secret.AuthenticationNeedsTwoFactor ||
				secret.AuthenticationChallengeID != "" ||
				secret.AuthenticationSkipTracker != test.wantSkip ||
				authenticator.loginCalls != test.wantLoginCalls {
				t.Fatalf("unavailable authentication result = %#v calls=%d", secret, authenticator.loginCalls)
			}
		})
	}
}

func TestCompositeAuthenticationFeedbackRemovesUnresolvedTracker(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:       "workflow-auth-skip",
			Revision: 2,
			Status:   api.WorkflowStatusBlocked,
			RequiredActions: []api.RequiredAction{{
				ID:               "action-auth-skip",
				Kind:             api.RequiredActionAuthenticateTracker,
				Status:           api.RequiredActionStatusPending,
				WorkflowRevision: 2,
				TrackerID:        "ALPHA",
			}},
		},
		Composite: &compositeUploadSession{
			Intent: api.WorkflowIntent{
				TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
			},
			FeedbackReceipts: make(map[string]compositeUploadFeedbackReceipt),
		},
	}
	module := &Module{}
	_, err := module.applyCompositeUploadFeedback(
		context.Background(),
		testOwnerID,
		&state,
		3,
		now,
		applyCompositeUploadFeedbackCommand{
			WorkflowID:       state.Workflow.ID,
			ExpectedRevision: 2,
			ActionID:         "action-auth-skip",
			IdempotencyKey:   "skip-unresolved-auth",
			Response: compositeUploadFeedbackResponse{
				Kind:                      api.ReleaseWorkflowUploadFeedbackTrackerAuthentication,
				TrackerID:                 "ALPHA",
				AuthenticationSkipTracker: true,
			},
		},
	)
	if err != nil {
		t.Fatalf("apply unresolved authentication feedback: %v", err)
	}
	if !slices.Equal(state.Composite.RemoveTrackers, []api.TrackerID{"ALPHA"}) ||
		len(state.Workflow.RequiredActions) != 0 ||
		state.Workflow.Status != api.WorkflowStatusActive {
		t.Fatalf("unresolved authentication state = %#v/%#v", state.Composite, state.Workflow)
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

type compositeUploadAuthTestFake struct {
	validate   api.TrackerAuthStatus
	login      api.TrackerAuthStatus
	loginCalls int
}

func (f *compositeUploadAuthTestFake) ValidateMany(
	_ context.Context,
	_ []string,
) ([]api.TrackerAuthStatus, error) {
	return []api.TrackerAuthStatus{f.validate}, nil
}

func (f *compositeUploadAuthTestFake) Login(
	_ context.Context,
	_ string,
	_ api.TrackerAuthLoginRequest,
) (api.TrackerAuthStatus, error) {
	f.loginCalls++
	return f.login, nil
}

func (*compositeUploadAuthTestFake) Submit2FA(
	context.Context,
	string,
	string,
) (api.TrackerAuthStatus, error) {
	return api.TrackerAuthStatus{}, nil
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

func waitCompositeUploadTestOperation(
	t *testing.T,
	module *Module,
	started CommandResult,
) CommandResult {
	t.Helper()
	if started.Operation == nil {
		t.Fatalf("composite upload has no operation: %#v", started)
	}
	deadline := time.Now().Add(5 * time.Second)
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
