// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
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

func TestCompositeUploadFeedbackHydratesPersistedMetadataDemand(t *testing.T) {
	t.Parallel()

	requirements := api.MetadataRequirementSet{
		Version: "composite-hydration-v1",
		Requirements: []api.MetadataRequirement{{
			Scope:       api.MetadataRequirementScopeAny,
			AnyOf:       []api.MetadataRequirementField{"original_title"},
			Disposition: api.RuleDispositionStrict,
		}},
	}
	basePreparer := testPreparer()
	hydrationInputs := make(chan api.PrepareInput, 1)
	preparer := basePreparer
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		if input.RequirePrepared {
			hydrationInputs <- input
			if !reflect.DeepEqual(input.MetadataRequirements, requirements) {
				return api.PrepareResult{}, errors.New("compatible prepared generation is required")
			}
		}
		return basePreparer.Prepare(ctx, input)
	}
	module, repository, _ := newCompositeUploadTestModule(t)
	module.preparer = preparer
	module.inputReadiness = compositeUploadDemandEvaluator{requirements: requirements}

	started, err := module.StartUpload(t.Context(), testOwnerID, compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "composite-hydration"))
	if err != nil {
		t.Fatalf("start composite debug upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	state, err := repository.Load(t.Context(), testOwnerID, blocked.Workflow.ID)
	if err != nil {
		t.Fatalf("load blocked composite state: %v", err)
	}
	if state.Composite == nil || state.Composite.Intent.Preparation == nil ||
		!reflect.DeepEqual(state.PreparationDemand, requirements) || !reflect.DeepEqual(state.Composite.Intent.Preparation.MetadataRequirements, api.MetadataRequirementSet{}) {
		t.Fatalf("persisted composite preparation state = %#v", state)
	}

	completed := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "approve-composite-hydration")
	if completed.Operation == nil || completed.Operation.Status != api.StageStatusCompleted || completed.DryRun == nil {
		t.Fatalf("resumed composite operation = %#v", completed)
	}
	select {
	case input := <-hydrationInputs:
		if input.SourcePath != blocked.Release.Release.Source.SourcePath || input.Force || !input.RequirePrepared ||
			input.Controls.ConfirmBDMVRescan || input.Controls.ForceRecheck != nil || !reflect.DeepEqual(input.MetadataRequirements, requirements) {
			t.Fatalf("composite hydration input = %#v", input)
		}
	default:
		t.Fatal("composite resume did not hydrate the prepared release")
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

func TestCompositeUploadReleaseNameFeedbackPreservesSiblingProjection(t *testing.T) {
	t.Parallel()

	module, _, _ := newCompositeUploadNameReviewTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "composite-name-review")
	request.Trackers.Include = []api.TrackerID{"ALPHA", "BETA", "GAMMA"}
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start name-review composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	actionIndex := slices.IndexFunc(blocked.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionProvideTrackerInput && action.TrackerID == "ALPHA" &&
			action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 || blocked.Projections == nil || blocked.Dupes == nil {
		t.Fatalf("initial name-review stop = %#v", blocked)
	}
	action := blocked.Continuation.RequiredActions[actionIndex]
	const reviewedName = "Example.Release.2026.REVIEWED-GRP"
	resumed, err := module.SubmitUploadFeedback(context.Background(), testOwnerID, blocked.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackTrackerInput,
			TrackerInput: &api.ReleaseWorkflowUploadTrackerInput{
				TrackerID: "ALPHA",
				Projection: api.ReleaseWorkflowUploadTrackerProjection{
					UploadReleaseName: api.WorkflowPatch[string]{Present: true, Value: reviewedName},
				},
			},
		},
		IdempotencyKey: "confirm-alpha-name",
	})
	if err != nil {
		t.Fatalf("submit composite name review: %v", err)
	}
	next := waitCompositeUploadTestOperation(t, module, resumed)
	if next.Projections == nil || next.Dupes == nil {
		t.Fatalf("name review discarded projections: %#v", next)
	}
	if !slices.ContainsFunc(next.Projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.TrackerID == "ALPHA" && projection.UploadReleaseName == reviewedName
	}) {
		t.Fatalf("reviewed ALPHA projection = %#v", next.Projections.Projections)
	}
	if !slices.ContainsFunc(next.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionProvideTrackerInput && action.TrackerID == "BETA" &&
			action.Status == api.RequiredActionStatusPending
	}) {
		t.Fatalf("sibling name-review action = %#v", next.Continuation.RequiredActions)
	}
}

func TestCompositeUploadTrackerInputRejectsMismatchedTrackerWithoutMutation(t *testing.T) {
	t.Parallel()

	module, repository, _ := newCompositeUploadNameReviewTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeDebug, "composite-name-mismatch")
	request.Trackers.Include = []api.TrackerID{"ALPHA", "BETA", "GAMMA"}
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start name-review composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	actionIndex := slices.IndexFunc(blocked.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionProvideTrackerInput && action.TrackerID == "ALPHA" &&
			action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 {
		t.Fatalf("initial name-review stop = %#v", blocked)
	}
	action := blocked.Continuation.RequiredActions[actionIndex]
	before, err := repository.Load(context.Background(), testOwnerID, blocked.Workflow.ID)
	if err != nil {
		t.Fatalf("load state before mismatched feedback: %v", err)
	}

	_, err = module.SubmitUploadFeedback(context.Background(), testOwnerID, blocked.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackTrackerInput,
			TrackerInput: &api.ReleaseWorkflowUploadTrackerInput{
				TrackerID: " beta ",
				Projection: api.ReleaseWorkflowUploadTrackerProjection{
					UploadReleaseName: api.WorkflowPatch[string]{Present: true, Value: "Example.Release.2026.WRONG-GRP"},
				},
			},
		},
		IdempotencyKey: "mismatched-tracker-input",
	})
	if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "feedback tracker does not match action") {
		t.Fatalf("mismatched tracker feedback error = %v", err)
	}
	after, err := repository.Load(context.Background(), testOwnerID, blocked.Workflow.ID)
	if err != nil {
		t.Fatalf("load state after mismatched feedback: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("mismatched tracker feedback mutated state: before=%#v after=%#v", before, after)
	}
}

func TestCompositeUploadRuleAuthorizationResumesLiveUpload(t *testing.T) {
	t.Parallel()

	module, _, uploads := newCompositeUploadWaiverTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "composite-rule-authorization")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	actionIndex := slices.IndexFunc(blocked.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionAuthorizeRules && action.TrackerID == "ALPHA" &&
			action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 || blocked.UploadResult != nil || uploads.execution != nil {
		t.Fatalf("waivable-rule stop = %#v execution=%#v", blocked, uploads.execution)
	}
	action := blocked.Continuation.RequiredActions[actionIndex]
	resumed, err := module.SubmitUploadFeedback(context.Background(), testOwnerID, blocked.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackRuleAuthorization,
			RuleAuthorization: &api.ReleaseWorkflowUploadConfirmation{
				Confirmed: true,
			},
		},
		IdempotencyKey: "authorize-composite-rules",
	})
	if err != nil {
		t.Fatalf("submit composite rule authorization: %v", err)
	}
	review := waitCompositeUploadTestOperation(t, module, resumed)
	projectionIndex := slices.IndexFunc(review.Projections.Projections, func(projection api.TrackerReleaseProjection) bool {
		return projection.TrackerID == "ALPHA"
	})
	if projectionIndex < 0 || review.Projections.Projections[projectionIndex].RuleAuthorizationFingerprint == "" ||
		!review.Projections.Projections[projectionIndex].UploadReady {
		t.Fatalf("authorized composite projection = %#v", review.Projections)
	}
	completed := approveCompositeUploadTrackers(
		t,
		module,
		review,
		[]api.TrackerID{"ALPHA", "BETA"},
		"approve-authorized-trackers",
	)
	if completed.UploadResult == nil || completed.Operation == nil ||
		completed.Operation.Status != api.StageStatusExecuted || uploads.execution == nil || uploads.execution.executions != 1 {
		t.Fatalf("authorized composite upload = %#v execution=%#v", completed, uploads.execution)
	}
}

func TestCompositeUploadRuleRejectionSkipsOnlyThatTracker(t *testing.T) {
	t.Parallel()

	module, _, uploads := newCompositeUploadWaiverTestModule(t)
	request := compositeUploadTestRequest(true, api.ReleaseWorkflowUploadModeUpload, "composite-rule-rejection")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	actionIndex := slices.IndexFunc(blocked.Continuation.RequiredActions, func(action api.RequiredAction) bool {
		return action.Kind == api.RequiredActionAuthorizeRules && action.TrackerID == "ALPHA" &&
			action.Status == api.RequiredActionStatusPending
	})
	if actionIndex < 0 {
		t.Fatalf("tracker rule action = %#v", blocked.Continuation.RequiredActions)
	}
	action := blocked.Continuation.RequiredActions[actionIndex]
	resumed, err := module.SubmitUploadFeedback(context.Background(), testOwnerID, blocked.Workflow.ID, api.ReleaseWorkflowUploadFeedback{
		Action: api.ReleaseWorkflowUploadActionIdentity{
			ID:               action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
		},
		Response: api.ReleaseWorkflowUploadFeedbackResponse{
			Kind: api.ReleaseWorkflowUploadFeedbackRuleAuthorization,
			RuleAuthorization: &api.ReleaseWorkflowUploadConfirmation{
				Confirmed: false,
			},
		},
		IdempotencyKey: "reject-composite-rules",
	})
	if err != nil {
		t.Fatalf("reject composite tracker rules: %v", err)
	}
	review := waitCompositeUploadTestOperation(t, module, resumed)
	approval := pendingCompositeTrackerApproval(t, review)
	if len(review.Selection.TrackerIDs) != 1 || review.Selection.TrackerIDs[0] != "BETA" ||
		review.Projections == nil || len(review.Projections.Projections) != 1 || review.Projections.Projections[0].TrackerID != "BETA" ||
		len(approval.Options) != 1 || approval.Options[0].Value != "BETA" || uploads.execution != nil {
		t.Fatalf(
			"tracker rule rejection = selection=%#v projections=%#v approval=%#v execution=%#v",
			review.Selection,
			review.Projections,
			approval,
			uploads.execution,
		)
	}
}

func TestCompositeUploadStrictUnattendedSkipsWaivableTracker(t *testing.T) {
	t.Parallel()

	module, _, uploads := newCompositeUploadWaiverTestModule(t)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "composite-skip-waivable")
	started, err := module.StartUpload(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	action := pendingCompositeTrackerApproval(t, blocked)
	if len(action.Options) != 1 || action.Options[0].Value != "BETA" || uploads.execution != nil ||
		slices.ContainsFunc(blocked.Continuation.RequiredActions, func(action api.RequiredAction) bool {
			return action.Kind == api.RequiredActionAuthorizeRules
		}) {
		t.Fatalf("strict unattended waiver handling = %#v execution=%#v", blocked, uploads.execution)
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

func TestCompositeUploadRefreshesOnlyRecoverablePersistedMediaBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		failures         []api.WorkflowFailure
		additionalAction *api.RequiredAction
		wantDescriptions bool
	}{
		{
			name:             "surviving tracker continues to descriptions",
			failures:         []api.WorkflowFailure{compositeImageHostFailure("BETA")},
			wantDescriptions: true,
		},
		{
			name:     "genuine tracker action remains blocked",
			failures: []api.WorkflowFailure{compositeImageHostFailure("BETA")},
			additionalAction: &api.RequiredAction{
				ID:             "action-tracker-input",
				Kind:           api.RequiredActionProvideTrackerInput,
				Status:         api.RequiredActionStatusPending,
				TrackerID:      "ALPHA",
				Prompt:         "Provide the required tracker value.",
				AllowsFreeText: true,
			},
		},
		{
			name: "all tracker hosts failed",
			failures: []api.WorkflowFailure{
				compositeImageHostFailure("ALPHA"),
				compositeImageHostFailure("BETA"),
			},
		},
		{
			name:     "unscoped host failure remains blocked",
			failures: []api.WorkflowFailure{compositeImageHostFailure("")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, repository, _ := newCompositeUploadTestModule(t)
			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "persisted-media-"+strings.ReplaceAll(test.name, " ", "-"))
			started, err := module.StartUpload(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatalf("start composite upload: %v", err)
			}
			blocked := waitCompositeUploadTestOperation(t, module, started)
			completed := approveCompositeUploadTrackers(
				t,
				module,
				blocked,
				[]api.TrackerID{"ALPHA", "BETA"},
				"approve-persisted-media-"+strings.ReplaceAll(test.name, " ", "-"),
			)
			command, operationID, initialRevision, initialMediaCount := seedPersistedCompositeMediaBlock(
				t,
				repository,
				completed.Workflow.ID,
				test.failures,
				test.additionalAction,
			)
			ctx := context.WithValue(t.Context(), operationExecutionContextKey{}, operationID)
			result, err := module.runCompositeUpload(ctx, testOwnerID, command)
			if err != nil {
				t.Fatalf("resume persisted media block: %v", err)
			}
			state, err := repository.Load(t.Context(), testOwnerID, completed.Workflow.ID)
			if err != nil {
				t.Fatalf("load resumed composite state: %v", err)
			}
			if test.wantDescriptions {
				if result.Descriptions == nil || result.Media == nil || result.Media.Status != api.StageStatusCompleted {
					t.Fatalf("recovered composite result = %#v", result)
				}
				if _, failed := TrackerImageHostFailure(*result.Media, "BETA"); !failed {
					t.Fatalf("recovered media lost tracker-scoped failure: %#v", result.Media.Failures)
				}
				if len(state.Media) != initialMediaCount+1 {
					t.Fatalf("recovered media revisions = %d, want %d", len(state.Media), initialMediaCount+1)
				}
				return
			}
			if result.Descriptions != nil || result.Media == nil || result.Media.Status != api.StageStatusBlocked {
				t.Fatalf("blocked composite result = %#v", result)
			}
			if state.Workflow.Revision != initialRevision+1 {
				t.Fatalf("blocked composite revision = %d, want %d (single terminal checkpoint)", state.Workflow.Revision, initialRevision+1)
			}
			if len(state.Media) != initialMediaCount {
				t.Fatalf("blocked composite refreshed media %d times, want none", len(state.Media)-initialMediaCount)
			}
		})
	}
}

func TestNormalizeCompositeUploadRequestPreservesManualFramesIntent(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		frames     []int
		wantFrames []int
	}{
		{name: "no_manual_frames", frames: []int{}},
		{
			name:       "manual_frames",
			frames:     []int{120, 240},
			wantFrames: []int{120, 240},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			count := 4
			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "media-intent-"+testCase.name)
			request.Media.Screenshots.Count = &count
			request.Media.Screenshots.Frames = testCase.frames
			session, _, err := normalizeCompositeUploadRequest(request)
			if err != nil {
				t.Fatalf("normalize composite upload request: %v", err)
			}
			if session.Intent.Media == nil || session.Intent.Media.ScreenshotCount != 0 {
				t.Fatalf("normalized media intent = %#v", session.Intent.Media)
			}
			if !reflect.DeepEqual(session.Intent.Media.ManualFrames, testCase.wantFrames) {
				t.Fatalf("normalized manual frames = %#v, want %#v", session.Intent.Media.ManualFrames, testCase.wantFrames)
			}
		})
	}
}

func TestNormalizeCompositeUploadRequestRetainsOptionalScreenshotCount(t *testing.T) {
	t.Parallel()

	zero, lower := 0, 1
	for _, test := range []struct {
		name    string
		count   *int
		present bool
		want    int
	}{
		{name: "omitted"},
		{
			name:    "zero",
			count:   &zero,
			present: true,
		},
		{
			name:    "lower",
			count:   &lower,
			present: true,
			want:    lower,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "screenshot-count-"+test.name)
			request.Media.Screenshots.Count = test.count
			session, _, err := normalizeCompositeUploadRequest(request)
			if err != nil {
				t.Fatalf("normalize composite upload request: %v", err)
			}
			if (session.RequestedScreenshotCount != nil) != test.present {
				t.Fatalf("retained screenshot count = %#v, want present=%t", session.RequestedScreenshotCount, test.present)
			}
			for _, trackerID := range request.Trackers.Include {
				instruction, ok := session.Intent.ProjectionInstructions[trackerID]
				if ok != test.present || (ok && (instruction.ScreenshotCount == nil || *instruction.ScreenshotCount != test.want)) {
					t.Fatalf("projection instruction for %s = %#v, want screenshot count present=%t value=%d", trackerID, instruction, test.present, test.want)
				}
			}
		})
	}
}

func TestCompositeUploadDynamicSelectionSeedsRequestedScreenshotCount(t *testing.T) {
	t.Parallel()

	zero, lower := 0, 1
	for _, test := range []struct {
		name    string
		count   *int
		present bool
		want    int
	}{
		{name: "omitted"},
		{
			name:    "zero",
			count:   &zero,
			present: true,
		},
		{
			name:    "lower",
			count:   &lower,
			present: true,
			want:    lower,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			module, _, _ := newCompositeUploadTestModule(t)
			module.mediaBuilder = mediaArtifactBuilderFunc(func(
				_ context.Context,
				_ api.ReleaseRef,
				projections api.TrackerReleaseProjectionSet,
				instructions api.MediaCaptureInstructions,
				_ time.Time,
			) (api.MediaArtifactSet, any, error) {
				requirements, err := mediaRequirementsFingerprint(projections.Projections)
				if err != nil {
					return api.MediaArtifactSet{}, nil, err
				}
				if instructions.ScreenshotCount != 0 {
					t.Fatalf("composite capture screenshot count = %d, want tracker projection requirements only", instructions.ScreenshotCount)
				}
				count := 0
				for _, projection := range projections.Projections {
					count = max(count, projection.Artifacts.ScreenshotCount)
				}
				artifacts := make([]api.MediaArtifact, 0, count+1)
				for index := range count {
					artifacts = append(artifacts, api.MediaArtifact{
						ID:       api.PublicResourceID(fmt.Sprintf("screenshot-%d", index)),
						Kind:     api.MediaArtifactScreenshot,
						Purpose:  api.ScreenshotPurposeFinal,
						Selected: true,
						Index:    index,
						Order:    index,
					})
				}
				var attempts []api.HostedImageAttempt
				if count > 0 {
					hosted := api.MediaArtifact{
						ID:       "hosted-screenshot-0",
						Kind:     api.MediaArtifactHostedImage,
						Purpose:  api.ScreenshotPurposeFinal,
						Selected: true,
						Source:   "screenshot-0",
					}
					artifacts = append(artifacts, hosted)
					trackerIDs := make([]api.TrackerID, 0, len(projections.Projections))
					for _, projection := range projections.Projections {
						trackerIDs = append(trackerIDs, projection.TrackerID)
					}
					attempts = []api.HostedImageAttempt{{
						ID:         "host-screenshot-0",
						UsageScope: "global",
						TrackerIDs: trackerIDs,
						Results:    []api.MediaArtifact{hosted},
					}}
				}
				return api.MediaArtifactSet{
					CaptureFingerprint:        testFingerprint(t, "dynamic-screenshot-count-"+test.name),
					RequirementsFingerprint:   requirements,
					Artifacts:                 artifacts,
					HostAttempts:              attempts,
					ImageRequirementsPrepared: true,
				}, struct{}{}, nil
			})
			base := module.trackerProjector
			var builds []map[api.TrackerID]*int
			module.trackerProjector = trackerProjectionBuilderFunc(func(
				ctx context.Context,
				release api.ReleaseSnapshot,
				subject api.UploadSubject,
				trackerIDs []api.TrackerID,
				instructions map[api.TrackerID]api.TrackerProjectionInstructions,
				ruleAuthorizations map[api.TrackerID]api.WorkflowFingerprint,
				executionMode api.WorkflowExecutionMode,
			) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
				captured := make(map[api.TrackerID]*int, len(instructions))
				for trackerID, instruction := range instructions {
					captured[trackerID] = cloneIntPointer(instruction.ScreenshotCount)
				}
				builds = append(builds, captured)
				catalog, runtime, selection, projections, err := base.Build(ctx, release, subject, trackerIDs, instructions, ruleAuthorizations, executionMode)
				if err != nil {
					return catalog, runtime, selection, projections, fmt.Errorf("build test tracker projections: %w", err)
				}
				for index := range projections.Projections {
					if count := instructions[projections.Projections[index].TrackerID].ScreenshotCount; count != nil {
						projections.Projections[index].Artifacts.ScreenshotCount = max(projections.Projections[index].Artifacts.ScreenshotCount, *count)
					}
				}
				return catalog, runtime, selection, projections, nil
			})

			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "dynamic-screenshot-count-"+test.name)
			request.Trackers.Include = nil
			request.Media.Screenshots.Count = test.count
			started, err := module.StartUpload(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatalf("start composite upload: %v", err)
			}
			blocked := waitCompositeUploadTestOperation(t, module, started)
			completed := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "approve-dynamic-screenshot-count-"+test.name)
			if completed.Operation == nil || completed.Operation.Status != api.StageStatusCompleted || completed.DryRun == nil || completed.Media == nil {
				t.Fatalf("completed dynamic screenshot count upload = %#v", completed)
			}
			selectedScreenshots := 0
			for _, artifact := range completed.Media.Artifacts {
				if artifact.Selected && artifact.Kind == api.MediaArtifactScreenshot && artifact.Purpose == api.ScreenshotPurposeFinal {
					selectedScreenshots++
				}
			}
			if selectedScreenshots != test.want {
				t.Fatalf("selected dynamic screenshots = %d, want %d", selectedScreenshots, test.want)
			}
			if len(builds) == 0 {
				t.Fatal("composite upload did not project dynamically selected trackers")
			}
			latest := builds[len(builds)-1]
			for _, trackerID := range []api.TrackerID{"ALPHA", "BETA"} {
				count := latest[trackerID]
				if (count != nil) != test.present || (count != nil && *count != test.want) {
					t.Fatalf("latest projection screenshot count for %s = %#v, want present=%t value=%d builds=%#v", trackerID, count, test.present, test.want, builds)
				}
			}
		})
	}
}

func TestCompositeUploadRequestedScreenshotCountDoesNotCaptureWithoutProjectionRequirement(t *testing.T) {
	t.Parallel()

	module, _, _ := newCompositeUploadTestModule(t)
	var captureInstructions []api.MediaCaptureInstructions
	module.mediaBuilder = mediaArtifactBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseRef,
		projections api.TrackerReleaseProjectionSet,
		instructions api.MediaCaptureInstructions,
		_ time.Time,
	) (api.MediaArtifactSet, any, error) {
		captureInstructions = append(captureInstructions, instructions)
		requirements, err := mediaRequirementsFingerprint(projections.Projections)
		if err != nil {
			return api.MediaArtifactSet{}, nil, err
		}
		return api.MediaArtifactSet{
			CaptureFingerprint:        testFingerprint(t, "no-image-requested-screenshot-count"),
			RequirementsFingerprint:   requirements,
			ImageRequirementsPrepared: true,
			Status:                    api.StageStatusCompleted,
		}, struct{}{}, nil
	})
	count := 7
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "no-image-requested-screenshot-count")
	request.Media.Screenshots.Count = &count
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	completed := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "approve-no-image-requested-screenshot-count")
	if completed.Operation == nil || completed.Operation.Status != api.StageStatusCompleted || completed.DryRun == nil || completed.Media == nil {
		t.Fatalf("completed no-image screenshot count upload = %#v", completed)
	}
	if len(captureInstructions) == 0 {
		t.Fatal("no-image composite upload did not prepare media")
	}
	for _, instructions := range captureInstructions {
		if instructions.ScreenshotCount != 0 {
			t.Fatalf("no-image composite capture screenshot count = %d, want 0", instructions.ScreenshotCount)
		}
	}
	if len(completed.Media.Artifacts) != 0 {
		t.Fatalf("no-image composite artifacts = %#v, want none", completed.Media.Artifacts)
	}
}

func TestCompositeUploadSelectedScreenshotExcessRespectsTrackerMinimumAndOrder(t *testing.T) {
	t.Parallel()

	artifacts := []api.MediaArtifact{
		{
			ID:       "comparison",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Source:   "comparison",
		},
		{
			ID:       "third",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Order:    3,
		},
		{
			ID:       "first",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Order:    1,
		},
		{
			ID:       "second",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Order:    2,
		},
		{
			ID:       "menu",
			Kind:     api.MediaArtifactDVDMenu,
			Purpose:  api.ScreenshotPurposeMenu,
			Selected: true,
			Order:    0,
		},
	}
	if actual, expected := compositeUploadSelectedScreenshotExcess(artifacts, 2), []api.PublicResourceID{"third"}; !slices.Equal(actual, expected) {
		t.Fatalf("minimum-preserving screenshot excess = %#v, want %#v", actual, expected)
	}
	if actual, expected := compositeUploadSelectedScreenshotExcess(artifacts, 0), []api.PublicResourceID{"first", "second", "third"}; !slices.Equal(actual, expected) {
		t.Fatalf("zero-count screenshot excess = %#v, want %#v", actual, expected)
	}
}

func TestCompositeUploadAutomaticPolicyDeselectsExcessAutomaticScreenshots(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		count int
		want  []api.PublicResourceID
	}{
		{
			name:  "lower",
			count: 1,
			want:  []api.PublicResourceID{"comparison", "first"},
		},
		{
			name:  "zero",
			count: 0,
			want:  []api.PublicResourceID{"comparison"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			module, repository, _ := newCompositeUploadTestModule(t)
			request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "trim-automatic-screenshots-"+test.name)
			started, err := module.StartUpload(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatalf("start composite upload: %v", err)
			}
			blocked := waitCompositeUploadTestOperation(t, module, started)
			completed := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "approve-trim-automatic-screenshots-"+test.name)

			const operationID api.WorkflowOperationID = "trim-automatic-screenshots"
			if err := module.private.Put(
				testOwnerID,
				completed.Workflow.ID,
				mediaPrivateResourceID(completed.Media.ID),
				&retainedMediaResourceFake{stats: &retainedMediaResourceStats{}},
				module.clock.Now().UTC().Add(time.Hour),
			); err != nil {
				t.Fatalf("replace retained test media: %v", err)
			}
			repository.mu.Lock()
			state := repository.states[completed.Workflow.ID]
			media := state.Media[state.Workflow.Media.ID]
			media.Artifacts = []api.MediaArtifact{
				{
					ID:       "comparison",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: true,
					Source:   "comparison",
				},
				{
					ID:       "third",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: true,
					Order:    3,
				},
				{
					ID:       "first",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: true,
					Order:    1,
				},
				{
					ID:       "second",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: true,
					Order:    2,
				},
				{
					ID:       "menu",
					Kind:     api.MediaArtifactDVDMenu,
					Purpose:  api.ScreenshotPurposeMenu,
					Selected: true,
				},
			}
			state.Media[media.ID] = media
			state.Composite.ActiveOperationID = operationID
			state.Composite.RequestedScreenshotCount = &test.count
			instructions := map[api.TrackerID]api.TrackerProjectionInstructions{}
			for _, trackerID := range state.Selections[state.Workflow.Selection.ID].TrackerIDs {
				instructions[trackerID] = api.TrackerProjectionInstructions{ScreenshotCount: cloneIntPointer(&test.count)}
			}
			state.Composite.Intent.ProjectionInstructions = instructions
			instructionSnapshot := state.ProjectionInstructions[state.Workflow.ProjectionInstructions.ID]
			instructionSnapshot.Instructions = instructions
			state.ProjectionInstructions[instructionSnapshot.ID] = instructionSnapshot
			projectionSnapshot := state.Projections[state.Workflow.TrackerProjections.ID]
			for index := range projectionSnapshot.Projections {
				projectionSnapshot.Projections[index].Artifacts.ScreenshotCount = test.count
			}
			state.Projections[projectionSnapshot.ID] = projectionSnapshot
			repository.states[completed.Workflow.ID] = state
			repository.mu.Unlock()

			current, err := module.Current(t.Context(), testOwnerID, completed.Workflow.ID)
			if err != nil {
				t.Fatalf("load composite upload: %v", err)
			}
			updated, err := repository.Load(t.Context(), testOwnerID, completed.Workflow.ID)
			if err != nil {
				t.Fatalf("load composite state: %v", err)
			}
			changed, err := module.applyCompositeAutomaticPolicy(t.Context(), testOwnerID, current, updated.Composite, operationID)
			if err != nil || !changed {
				t.Fatalf("trim automatic screenshots changed=%t err=%v", changed, err)
			}
			after, err := module.Current(t.Context(), testOwnerID, completed.Workflow.ID)
			if err != nil {
				t.Fatalf("load trimmed media: %v", err)
			}
			if after.Media == nil {
				t.Fatal("trimmed composite media is unavailable")
			}
			selectedScreenshots := make([]api.PublicResourceID, 0)
			menuSelected := false
			for _, artifact := range after.Media.Artifacts {
				if artifact.Selected && artifact.Kind == api.MediaArtifactScreenshot && artifact.Purpose == api.ScreenshotPurposeFinal {
					selectedScreenshots = append(selectedScreenshots, artifact.ID)
				}
				if artifact.Selected && artifact.Kind == api.MediaArtifactDVDMenu {
					menuSelected = true
				}
			}
			if !slices.Equal(selectedScreenshots, test.want) || !menuSelected {
				t.Fatalf("trimmed media screenshots=%#v menuSelected=%t", selectedScreenshots, menuSelected)
			}
		})
	}
}

func seedPersistedCompositeMediaBlock(
	t *testing.T,
	repository *MemoryRepository,
	workflowID api.WorkflowID,
	failures []api.WorkflowFailure,
	additionalAction *api.RequiredAction,
) (CompositeUploadCommand, api.WorkflowOperationID, api.WorkflowRevision, int) {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	state := repository.states[workflowID]
	if state.Composite == nil || state.Workflow.Media == nil {
		t.Fatalf("completed composite state is missing session or media: %#v", state.Workflow)
	}
	media := state.Media[state.Workflow.Media.ID]
	action := api.RequiredAction{
		ID:               "action-media-input",
		Kind:             api.RequiredActionProvideTrackerInput,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: state.Workflow.Revision,
		Prompt:           "Capture, select, or host the required release images before continuing.",
		CreatedAt:        state.Workflow.UpdatedAt,
	}
	media.Artifacts = []api.MediaArtifact{
		{
			ID:       "screen-0",
			Kind:     api.MediaArtifactScreenshot,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
		},
		{
			ID:       "hosted-0",
			Kind:     api.MediaArtifactHostedImage,
			Purpose:  api.ScreenshotPurposeFinal,
			Selected: true,
			Source:   "screen-0",
		},
	}
	media.HostAttempts = []api.HostedImageAttempt{{
		UsageScope: "global",
		TrackerIDs: []api.TrackerID{"ALPHA"},
		Results:    []api.MediaArtifact{media.Artifacts[1]},
	}}
	if intent := state.Composite.Intent.Media; intent != nil {
		fingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(struct {
			Release      api.ReleaseRef
			ProjectionID api.TrackerReleaseProjectionSetID
			Revision     api.WorkflowRevision
			Instructions api.MediaCaptureInstructions
			Requirements api.WorkflowFingerprint
		}{
			Release:      state.Projections[state.Workflow.TrackerProjections.ID].ReleaseRef,
			ProjectionID: state.Workflow.TrackerProjections.ID,
			Revision:     state.Workflow.TrackerProjections.Revision,
			Instructions: *intent,
			Requirements: media.RequirementsFingerprint,
		})
		if fingerprintErr != nil {
			t.Fatalf("fingerprint persisted media fixture: %v", fingerprintErr)
		}
		media.CaptureFingerprint = fingerprint
	}
	media.ImageRequirementsPrepared = true
	media.Status = api.StageStatusBlocked
	media.Failures = append([]api.WorkflowFailure(nil), failures...)
	media.RequiredActions = []api.RequiredAction{action}
	if additionalAction != nil {
		genuine := *additionalAction
		genuine.WorkflowRevision = state.Workflow.Revision
		genuine.CreatedAt = state.Workflow.UpdatedAt
		media.RequiredActions = append(media.RequiredActions, genuine)
	}
	state.Media[media.ID] = media
	state.Workflow.Descriptions = nil
	state.Workflow.DryRun = nil
	state.Workflow.UploadResult = nil
	state.Workflow.Status = api.WorkflowStatusBlocked
	state.Workflow.RequiredActions = append([]api.RequiredAction(nil), media.RequiredActions...)
	state.Workflow.Failures = append([]api.WorkflowFailure(nil), failures...)
	operationID := api.WorkflowOperationID("operation-persisted-media")
	state.Composite.ActiveOperationID = operationID
	state.Composite.TerminalReason = ""
	state.Composite.Goal = api.WorkflowGoalDescriptionsReady
	repository.states[workflowID] = state
	return CompositeUploadCommand{
		WorkflowID:         workflowID,
		ExpectedRevision:   state.Workflow.Revision,
		SessionFingerprint: state.Composite.RequestFingerprint,
		IdempotencyKey:     "resume-persisted-media",
	}, operationID, state.Workflow.Revision, len(state.Media)
}

func compositeImageHostFailure(trackerID api.TrackerID) api.WorkflowFailure {
	return api.WorkflowFailure{
		Failure: api.OperationFailure{
			Code:      api.OperationFailureImageHostUnavailable,
			Operation: api.OperationKindImageHosting,
			Message:   "Required image host failed.",
			Recovery:  api.OperationRecoveryRetry,
		},
		TrackerID: trackerID,
		Resource:  "synthetic-host",
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

type compositeUploadDemandEvaluator struct {
	readyInputReadinessEvaluator
	requirements api.MetadataRequirementSet
}

func (e compositeUploadDemandEvaluator) Requirements(context.Context, []api.TrackerID) (api.MetadataRequirementSet, error) {
	return e.requirements, nil
}

func newCompositeUploadTestModule(
	t *testing.T,
) (*Module, *MemoryRepository, *uploadPlanBuilderFake) {
	return newCompositeUploadTestModuleConfigured(t, false, false)
}

func newCompositeUploadWaiverTestModule(
	t *testing.T,
) (*Module, *MemoryRepository, *uploadPlanBuilderFake) {
	return newCompositeUploadTestModuleConfigured(t, true, false)
}

func newCompositeUploadNameReviewTestModule(
	t *testing.T,
) (*Module, *MemoryRepository, *uploadPlanBuilderFake) {
	return newCompositeUploadTestModuleConfigured(t, false, true)
}

func newCompositeUploadTestModuleConfigured(
	t *testing.T,
	waivableRules bool,
	releaseNameReview bool,
) (*Module, *MemoryRepository, *uploadPlanBuilderFake) {
	t.Helper()
	projections := trackerProjectionBuilderFunc(func(
		ctx context.Context,
		_ api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		instructions map[api.TrackerID]api.TrackerProjectionInstructions,
		ruleAuthorizations map[api.TrackerID]api.WorkflowFingerprint,
		executionMode api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		if releaseNameReview {
			for _, instruction := range instructions {
				if instruction.UploadReleaseName.Present {
					if _, ok := logging.FromContext(ctx, nil).(api.NopLogger); !ok {
						return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
							errors.New("release-name projection rebuild did not suppress duplicate diagnostics")
					}
					break
				}
			}
		}
		if len(trackerIDs) == 0 {
			trackerIDs = []api.TrackerID{"ALPHA", "BETA"}
		}
		catalog := testCatalog(t)
		if releaseNameReview {
			catalog.Trackers = append(catalog.Trackers, api.TrackerCatalogDescriptor{
				TrackerID:         "GAMMA",
				DisplayName:       "Gamma",
				ProjectorVersion:  "v1",
				PolicyFingerprint: testFingerprint(t, "gamma-policy"),
			})
		}
		catalog.Trackers = slices.DeleteFunc(catalog.Trackers, func(descriptor api.TrackerCatalogDescriptor) bool {
			return !slices.Contains(trackerIDs, descriptor.TrackerID)
		})
		catalog, err := catalog.WithFingerprint()
		if err != nil {
			return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{},
				fmt.Errorf("fingerprint composite test catalog: %w", err)
		}
		runtime := testRuntime(t)
		if releaseNameReview {
			runtime.Trackers = append(runtime.Trackers, api.TrackerRuntimeEntry{
				TrackerID:         "GAMMA",
				Configured:        true,
				ConfigFingerprint: testFingerprint(t, "gamma-config"),
			})
		}
		runtime.Trackers = slices.DeleteFunc(runtime.Trackers, func(entry api.TrackerRuntimeEntry) bool {
			return !slices.Contains(trackerIDs, entry.TrackerID)
		})
		projected := make([]api.TrackerReleaseProjection, 0, len(trackerIDs))
		actions := make([]api.RequiredAction, 0, len(trackerIDs))
		projectionInput := "composite-projection-input"
		for _, trackerID := range trackerIDs {
			automaticName := "Example.Release.2026." + string(trackerID) + "-GRP"
			projection := testProjection(t, trackerID, automaticName)
			projection.DescriptionGroup = "alpha"
			if releaseNameReview && trackerID != "GAMMA" {
				projection.PolicyDecisions = []api.TrackerPolicyDecision{{
					Code:     releaseNameConfirmationDecisionCode,
					Decision: "confirmation_required",
				}}
				if instruction := instructions[trackerID]; instruction.UploadReleaseName.Present {
					projection.UploadReleaseName = instruction.UploadReleaseName.Value
					projection.AdditionalNames = []api.TrackerReleaseName{{
						Role:  api.TrackerReleaseNameRoleSearch,
						Value: automaticName,
					}}
					projection.ProjectorFingerprint = testFingerprint(t, string(trackerID)+"-projector-reviewed")
					projection.InputFingerprint = testFingerprint(t, string(trackerID)+"-input-reviewed")
					projection.PolicyDecisions[0].Decision = "confirmed"
					projectionInput += "-" + strings.ToLower(string(trackerID)) + "-reviewed"
				} else {
					projection.UploadReady = false
					projection.RequiredActions = []api.RequiredAction{{
						Kind:           api.RequiredActionProvideTrackerInput,
						TrackerID:      trackerID,
						Prompt:         "Confirm the tracker release name.",
						AllowsFreeText: true,
						Options: []api.RequiredActionOption{{
							Value: automaticName,
							Label: automaticName,
						}},
					}}
					actions = append(actions, projection.RequiredActions...)
					projectionInput += "-" + strings.ToLower(string(trackerID)) + "-pending"
				}
			}
			if waivableRules && trackerID == "ALPHA" {
				waivableFingerprint := testFingerprint(t, "composite-waivable-rules")
				projection.WaivableRuleFingerprint = waivableFingerprint
				projection.PolicyDecisions = []api.TrackerPolicyDecision{{
					Code:        "language_rule",
					Message:     "language waiver required",
					Disposition: api.RuleDispositionWaivable,
				}}
				if ruleAuthorizations[trackerID] == waivableFingerprint {
					projection.RuleAuthorizationFingerprint = waivableFingerprint
					projection.PolicyDecisions[0].Decision = "authorized"
					projectionInput = "composite-projection-input-authorized"
				} else {
					projection.Readiness = api.ReadinessStatusBlocked
					projection.DupeReady = false
					projection.UploadReady = false
					projection.PolicyDecisions[0].Decision = "authorization_required"
					projection.PolicyDecisions[0].Blocking = true
					projection.RequiredActions = []api.RequiredAction{{
						Kind:   api.RequiredActionAuthorizeRules,
						Prompt: "Alpha rule warning: language rule. Upload to this tracker anyway?",
					}}
					actions = append(actions, projection.RequiredActions...)
					projectionInput = "composite-projection-input-pending"
				}
			}
			projected = append(projected, projection)
		}
		return catalog, runtime, api.TrackerSelection{TrackerIDs: trackerIDs}, api.TrackerReleaseProjectionSet{
			InputFingerprint:  testFingerprint(t, projectionInput),
			PolicyFingerprint: testFingerprint(t, "composite-projection-policy"),
			ExecutionMode:     executionMode,
			Projections:       projected,
			Status:            api.StageStatusReady,
			RequiredActions:   actions,
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
	preflight := compositeUploadReadyPreflightBuilder(t)
	if releaseNameReview {
		preflight = compositeUploadNameReviewPreflightBuilder(t)
	}
	module, repository := newTestModule(
		t,
		testPreparer(),
		WithTrackerProjectionBuilder(projections),
		WithTrackerPreflightBuilder(preflight),
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

func compositeUploadNameReviewPreflightBuilder(t *testing.T) TrackerPreflightBuilder {
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
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("build name-review composite preflight: %w", err)
		}
		for index := range assessment.Results {
			projectionIndex := slices.IndexFunc(initial.Projections, func(projection api.TrackerReleaseProjection) bool {
				return projection.TrackerID == assessment.Results[index].TrackerID
			})
			if projectionIndex >= 0 {
				assessment.Results[index].RequiredActions = append(
					[]api.RequiredAction(nil),
					initial.Projections[projectionIndex].RequiredActions...,
				)
			}
		}
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
