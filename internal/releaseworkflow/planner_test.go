// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestContinueBeginsAndAdvancesThroughCentralPlanner(t *testing.T) {
	t.Parallel()

	module, _ := newTestModule(t, testPreparer())
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-prepared",
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			Preparation: &api.PrepareInput{
				SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
				Instructions: api.ReleaseFactInstructions{
					SourceLookup: "Example Release 2026",
				},
			},
		},
	}
	created, err := module.Continue(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("begin continuation: %v", err)
	}
	if created.Workflow.Revision != 1 || created.Release != nil || created.Continuation.Refs.Release != nil {
		t.Fatalf("created continuation = %#v", created)
	}

	request.Authority = &api.WorkflowAuthority{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
	}
	advanced, err := module.Continue(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("continue preparation: %v", err)
	}
	if advanced.Operation != nil && !isTerminalProgressStatus(advanced.Operation.Status) {
		waitForWorkflowOperation(t, module, created.Workflow.ID, advanced.Operation.ID, func(status api.WorkflowOperationStatus) bool {
			return isTerminalProgressStatus(status.Status)
		})
	}
	prepared, err := module.Current(context.Background(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("current prepared continuation: %v", err)
	}
	if prepared.Release == nil || prepared.Continuation.Refs.Release == nil ||
		*prepared.Continuation.Refs.Release != *prepared.Workflow.Release {
		t.Fatalf("prepared continuation = %#v", prepared)
	}

	if _, err := module.Continue(context.Background(), testOwnerID, request); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale continuation authority error = %v, want %v", err, ErrRevisionConflict)
	}
	request.Authority.ExpectedRevision = prepared.Workflow.Revision
	replayed, err := module.Continue(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("continue satisfied goal: %v", err)
	}
	if replayed.Workflow.Revision != prepared.Workflow.Revision || replayed.Release == nil || replayed.Release.ID != prepared.Release.ID {
		t.Fatalf("satisfied continuation changed state: %#v", replayed)
	}
}

func TestContinueCreationPersistsTrustedTrackerDecisionMode(t *testing.T) {
	t.Parallel()

	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-mode",
		Goal:           api.WorkflowGoalPrepared,
		Intent: api.WorkflowIntent{
			Preparation: &api.PrepareInput{
				SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
				Instructions: api.ReleaseFactInstructions{
					SourceLookup: "Example Release 2026",
				},
			},
		},
	}
	publicModule, publicRepository := newTestModule(t, testPreparer())
	publicCurrent, err := publicModule.Continue(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("create public continuation: %v", err)
	}
	publicState, err := publicRepository.Load(context.Background(), testOwnerID, publicCurrent.Workflow.ID)
	if err != nil {
		t.Fatalf("load public continuation: %v", err)
	}
	if publicState.TrackerDecisionMode != TrackerDecisionModePostDupeGate {
		t.Fatalf("public continuation tracker mode = %q", publicState.TrackerDecisionMode)
	}

	appModule, appRepository := newTestModule(t, testPreparer())
	appCurrent, err := appModule.Continue(
		WithTrackerDecisionMode(context.Background(), TrackerDecisionModeWebUIControls),
		testOwnerID,
		request,
	)
	if err != nil {
		t.Fatalf("create app continuation: %v", err)
	}
	appState, err := appRepository.Load(context.Background(), testOwnerID, appCurrent.Workflow.ID)
	if err != nil {
		t.Fatalf("load app continuation: %v", err)
	}
	if appState.TrackerDecisionMode != TrackerDecisionModeWebUIControls {
		t.Fatalf("app continuation tracker mode = %q", appState.TrackerDecisionMode)
	}
}

func TestContinueRefreshesOnlyRecoverablePersistedMediaBlockForWebUIControls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		failures         []api.WorkflowFailure
		additionalAction *api.RequiredAction
		wantDescriptions bool
	}{
		{
			name:             "surviving tracker reaches descriptions",
			failures:         []api.WorkflowFailure{compositeImageHostFailure("BETA")},
			wantDescriptions: true,
		},
		{
			name:     "genuine tracker action remains blocked",
			failures: []api.WorkflowFailure{compositeImageHostFailure("BETA")},
			additionalAction: &api.RequiredAction{
				ID:             "action-webui-tracker-input",
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
		{
			name:     "unknown tracker host failure remains blocked",
			failures: []api.WorkflowFailure{compositeImageHostFailure("GAMMA")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, repository, request, initialRevision, initialMediaCount := preparePersistedWebUIMediaContinuation(
				t,
				test.failures,
				test.additionalAction,
			)
			first, err := module.Continue(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatalf("continue persisted media: %v", err)
			}
			if !test.wantDescriptions {
				state, loadErr := repository.Load(t.Context(), testOwnerID, first.Workflow.ID)
				if loadErr != nil {
					t.Fatalf("load blocked continuation: %v", loadErr)
				}
				if first.Media == nil || first.Media.Status != api.StageStatusBlocked || first.Descriptions != nil {
					t.Fatalf("blocked WebUI continuation = %#v", first)
				}
				if state.Workflow.TrackerApproval != nil || state.Workflow.Revision != initialRevision || len(state.Media) != initialMediaCount {
					t.Fatalf("blocked WebUI continuation churned state: workflow=%#v media=%d", state.Workflow, len(state.Media))
				}
				return
			}
			if first.Media == nil || first.Media.Status != api.StageStatusCompleted || first.Descriptions != nil {
				t.Fatalf("first recovered WebUI transition = %#v", first)
			}
			request.Authority.ExpectedRevision = first.Workflow.Revision
			describing, err := module.Continue(t.Context(), testOwnerID, request)
			if err != nil {
				t.Fatalf("continue recovered media to descriptions: %v", err)
			}
			if describing.Operation != nil && !isTerminalProgressStatus(describing.Operation.Status) {
				waitForWorkflowOperation(t, module, first.Workflow.ID, describing.Operation.ID, func(status api.WorkflowOperationStatus) bool {
					return isTerminalProgressStatus(status.Status)
				})
			}
			active, activeErr := repository.ListActiveOperations(t.Context())
			if activeErr != nil {
				t.Fatalf("list active description operations: %v", activeErr)
			}
			for _, record := range active {
				if record.WorkflowID != first.Workflow.ID {
					continue
				}
				waitForWorkflowOperation(t, module, first.Workflow.ID, record.OperationID, func(status api.WorkflowOperationStatus) bool {
					return isTerminalProgressStatus(status.Status)
				})
			}
			current, err := module.Current(t.Context(), testOwnerID, first.Workflow.ID)
			if err != nil {
				t.Fatalf("load described WebUI continuation: %v", err)
			}
			if current.Descriptions == nil || len(current.Descriptions.Descriptions) != 1 ||
				!slices.Equal(current.Descriptions.Descriptions[0].TrackerIDs, []api.TrackerID{"ALPHA"}) {
				t.Fatalf("recovered WebUI descriptions retained failed lane: %#v", current.Descriptions)
			}
			if current.TrackerApproval != nil || current.Media == nil {
				t.Fatalf("WebUI recovery unexpectedly required approval: approval=%#v media=%#v", current.TrackerApproval, current.Media)
			}
			if _, failed := TrackerImageHostFailure(*current.Media, "BETA"); !failed {
				t.Fatalf("recovered WebUI media lost scoped failure: %#v", current.Media.Failures)
			}
		})
	}
}

func TestContinueExpiredDupesReplansBeforePersistedMediaRecovery(t *testing.T) {
	t.Parallel()

	module, repository, request, initialRevision, initialMediaCount := preparePersistedWebUIMediaContinuation(
		t,
		[]api.WorkflowFailure{compositeImageHostFailure("BETA")},
		nil,
	)
	state, err := repository.Load(t.Context(), testOwnerID, request.Authority.WorkflowID)
	if err != nil {
		t.Fatalf("load persisted media state: %v", err)
	}
	dupes := state.Dupes[state.Workflow.Dupes.ID]
	dupes.ExpiresAt = module.clock.Now().UTC().Add(-time.Minute)
	repository.mu.Lock()
	state.Dupes[dupes.ID] = dupes
	repository.states[state.Workflow.ID] = state
	repository.mu.Unlock()

	base := module.dupeBuilder
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	module.dupeBuilder = dupeAssessmentBuilderFunc(func(
		ctx context.Context,
		subject api.DuplicateSubject,
		projections api.TrackerReleaseProjectionSet,
		preflight api.TrackerPreflightAssessment,
		now time.Time,
		skipRemote bool,
	) (api.DupeAssessment, any, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return base.Build(ctx, subject, projections, preflight, now, skipRemote)
	})

	replanning, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("continue expired duplicate evidence: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate replanning did not start")
	}
	active, err := repository.ListActiveOperations(t.Context())
	if err != nil {
		t.Fatalf("list duplicate replanning operations: %v", err)
	}
	operationIndex := slices.IndexFunc(active, func(record api.ReleaseWorkflowOperationRecord) bool {
		return record.WorkflowID == request.Authority.WorkflowID && record.Status.Command == (CheckDuplicatesCommand{}).commandName()
	})
	if operationIndex < 0 {
		t.Fatalf("expired duplicate continuation = %#v", replanning.Operation)
	}
	replanningOperation := active[operationIndex]
	retained, err := repository.Load(t.Context(), testOwnerID, request.Authority.WorkflowID)
	if err != nil {
		t.Fatalf("load duplicate replanning state: %v", err)
	}
	if retained.Workflow.Revision != initialRevision || retained.Workflow.Media == nil ||
		*retained.Workflow.Media != *state.Workflow.Media || len(retained.Media) != initialMediaCount {
		t.Fatalf("expired duplicate evidence refreshed stale media: workflow=%#v media=%d", retained.Workflow, len(retained.Media))
	}
	releaseOnce.Do(func() { close(release) })
	waitForWorkflowOperation(t, module, retained.Workflow.ID, replanningOperation.OperationID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
}

func TestRefreshPersistedMediaStatusRejectsEmptyApprovedTrackerSet(t *testing.T) {
	t.Parallel()

	module, repository, request, initialRevision, initialMediaCount := preparePersistedWebUIMediaContinuation(
		t,
		[]api.WorkflowFailure{compositeImageHostFailure("BETA")},
		nil,
	)
	state, err := repository.Load(t.Context(), testOwnerID, request.Authority.WorkflowID)
	if err != nil {
		t.Fatalf("load persisted media state: %v", err)
	}
	state.TrackerDecisionMode = TrackerDecisionModePostDupeGate
	now := module.clock.Now().UTC()
	_, dupes, candidateIDs, err := trackerApprovalCandidates(&state, now)
	if err != nil {
		t.Fatalf("resolve tracker approval candidates: %v", err)
	}
	if !slices.Equal(candidateIDs, []api.TrackerID{"ALPHA", "BETA"}) {
		t.Fatalf("tracker approval candidates = %#v", candidateIDs)
	}
	actionInput, err := trackerApprovalActionInput(&state, dupes, candidateIDs)
	if err != nil {
		t.Fatalf("fingerprint tracker approval action: %v", err)
	}
	approvedIDs := []api.TrackerID{}
	approvalFingerprint, err := trackerApprovalFingerprint(actionInput, candidateIDs, approvedIDs)
	if err != nil {
		t.Fatalf("fingerprint empty tracker approval: %v", err)
	}
	// Simulate legacy persisted authority with current lineage but no approved
	// trackers. New approval commands reject this state; reads must not advance it.
	approval := api.TrackerApprovalSnapshot{
		ID:                  "tracker-approval-empty",
		WorkflowID:          state.Workflow.ID,
		Revision:            state.Workflow.Revision,
		Release:             *state.Workflow.Release,
		Selection:           *state.Workflow.Selection,
		ProjectionSet:       *state.Workflow.TrackerProjections,
		Preflight:           *state.Workflow.TrackerPreflight,
		Dupes:               *state.Workflow.Dupes,
		CandidateTrackerIDs: append([]api.TrackerID(nil), candidateIDs...),
		ApprovedTrackerIDs:  approvedIDs,
		InputFingerprint:    approvalFingerprint,
		CreatedAt:           now,
	}
	approvalRef := api.TrackerApprovalSnapshotRef{ID: approval.ID, Revision: approval.Revision}
	media := state.Media[state.Workflow.Media.ID]
	media.TrackerApproval = &approvalRef
	media.Failures = nil
	state.Media[media.ID] = media
	state.TrackerApprovals[approval.ID] = approval
	state.Workflow.TrackerApproval = &approvalRef
	state.Workflow.Failures = nil
	repository.mu.Lock()
	repository.states[state.Workflow.ID] = state
	repository.mu.Unlock()

	withoutGuard := media
	refreshMutatedMediaStatus(&withoutGuard, nil)
	if withoutGuard.Status != api.StageStatusCompleted {
		t.Fatalf("empty tracker fixture remains blocked without guard: %#v", withoutGuard)
	}
	_, err = module.execute(t.Context(), testOwnerID, refreshPersistedMediaStatusCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: state.Workflow.Revision,
		Media:            *state.Workflow.Media,
		IdempotencyKey:   "refresh-empty-approved-trackers",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("refresh persisted media error = %v, want %v", err, ErrInvalidTransition)
	}
	retained, err := repository.Load(t.Context(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatalf("reload persisted media state: %v", err)
	}
	if retained.Workflow.Revision != initialRevision || len(retained.Media) != initialMediaCount {
		t.Fatalf("rejected persisted media refresh churned state: workflow=%#v media=%d", retained.Workflow, len(retained.Media))
	}
}

func preparePersistedWebUIMediaContinuation(
	t *testing.T,
	failures []api.WorkflowFailure,
	additionalAction *api.RequiredAction,
) (*Module, *MemoryRepository, api.ContinueReleaseWorkflowRequest, api.WorkflowRevision, int) {
	t.Helper()
	module, repository, _ := newCompositeUploadTestModule(t)
	requestID := "webui-persisted-media-" + string(failures[0].TrackerID)
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, requestID)
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("start setup composite upload: %v", err)
	}
	blocked := waitCompositeUploadTestOperation(t, module, started)
	completed := approveCompositeUploadTrackers(t, module, blocked, []api.TrackerID{"ALPHA", "BETA"}, "approve-"+requestID)
	_, _, initialRevision, initialMediaCount := seedPersistedCompositeMediaBlock(
		t,
		repository,
		completed.Workflow.ID,
		failures,
		additionalAction,
	)
	repository.mu.Lock()
	state := repository.states[completed.Workflow.ID]
	state.TrackerDecisionMode = TrackerDecisionModeWebUIControls
	state.Workflow.TrackerApproval = nil
	media := state.Media[state.Workflow.Media.ID]
	media.TrackerApproval = nil
	state.Media[media.ID] = media
	state.Composite.ActiveOperationID = ""
	intent := state.Composite.Intent
	repository.states[completed.Workflow.ID] = state
	repository.mu.Unlock()
	return module, repository, api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       completed.Workflow.ID,
			ExpectedRevision: initialRevision,
		},
		IdempotencyKey: requestID + "-continue",
		Goal:           api.WorkflowGoalDescriptionsReady,
		Intent:         intent,
	}, initialRevision, initialMediaCount
}

func TestContinuationNameReviewBlocksUploadButNotDuplicateChecking(t *testing.T) {
	t.Parallel()

	action := api.RequiredAction{
		ID:        "action-name-review",
		Kind:      api.RequiredActionProvideTrackerInput,
		Status:    api.RequiredActionStatusPending,
		TrackerID: "ALPHA",
	}
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{
			ID:              "workflow-name-review",
			Revision:        4,
			RequiredActions: []api.RequiredAction{action},
		},
		Projections: &api.TrackerReleaseProjectionSet{
			Projections: []api.TrackerReleaseProjection{{
				TrackerID:   "ALPHA",
				Readiness:   api.ReadinessStatusReady,
				DupeReady:   true,
				UploadReady: false,
			}},
		},
	}
	module := &Module{}
	request := api.ContinueReleaseWorkflowRequest{
		Goal: api.WorkflowGoalDuplicatesDecided,
	}
	_, handled, err := module.resolveContinuationAnswer(
		context.Background(),
		testOwnerID,
		request,
		current,
		TrackerDecisionModeWebUIControls,
	)
	if err != nil || handled {
		t.Fatalf("duplicate continuation blocked by name review: handled=%t err=%v", handled, err)
	}

	request.Goal = api.WorkflowGoalMediaReady
	blocked, handled, err := module.resolveContinuationAnswer(
		context.Background(),
		testOwnerID,
		request,
		current,
		TrackerDecisionModeWebUIControls,
	)
	if err != nil || !handled || blocked.Workflow.ID != current.Workflow.ID {
		t.Fatalf("downstream continuation was not blocked by name review: handled=%t err=%v", handled, err)
	}
}

func TestContinuationPlannerInsertsExactImageRequirementBarrier(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-plan", Revision: 7},
		Release:  &api.ReleaseSnapshot{ID: "release-plan", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{}},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-plan",
			Revision:         3,
			InputFingerprint: testFingerprint(t, "projection-input"),
			Projections: []api.TrackerReleaseProjection{{
				TrackerID:   "ALPHA",
				Readiness:   api.ReadinessStatusReady,
				DupeReady:   true,
				UploadReady: true,
			}},
		},
		Preflight: &api.TrackerPreflightAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-plan", Revision: 3},
			ExpiresAt:     now.Add(time.Hour),
		},
		Dupes: &api.DupeAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-plan", Revision: 3},
			Status:        api.StageStatusCompleted,
			ExpiresAt:     now.Add(time.Hour),
			Results: []api.TrackerDupeAssessment{{
				TrackerID: "ALPHA",
				Status:    api.StageStatusCompleted,
				Decision:  api.DupeDecisionNoMatch,
			}},
		},
		Media: &api.MediaArtifactSet{
			ID:       "media-plan",
			Revision: 6,
			Status:   api.StageStatusCompleted,
			Artifacts: []api.MediaArtifact{{
				ID:       "screen-plan",
				Kind:     api.MediaArtifactScreenshot,
				Selected: true,
			}},
		},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-descriptions",
		Goal:           api.WorkflowGoalDescriptionsReady,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}
	command, stage := planContinuationCommand(request, current, now)
	upload, ok := command.(UploadMediaImagesCommand)
	if !ok || stage != "prepare-image-requirements" || upload.Host != "" || upload.Media.ID != current.Media.ID {
		t.Fatalf("planned image barrier: stage=%q command=%#v", stage, command)
	}

	current.Media.ImageRequirementsPrepared = true
	current.Media.Artifacts = append(current.Media.Artifacts, api.MediaArtifact{
		ID:       "hosted-screen-plan",
		Kind:     api.MediaArtifactHostedImage,
		Purpose:  api.ScreenshotPurposeFinal,
		Selected: true,
		Source:   "screen-plan",
	})
	request.Intent.Descriptions = &api.DescriptionInstructions{TemplateVersion: "workflow-v1"}
	command, stage = planContinuationCommand(request, current, now)
	if _, ok := command.(GenerateDescriptionsCommand); !ok || stage != "generate-descriptions" {
		t.Fatalf("planned descriptions after image hosting: stage=%q command=%#v", stage, command)
	}
	if slices.ContainsFunc(current.Media.Artifacts, func(artifact api.MediaArtifact) bool {
		return artifact.Kind == api.MediaArtifactDVDMenu
	}) {
		t.Fatalf("optional DVD menu unexpectedly entered continuation media: %#v", current.Media.Artifacts)
	}

	request.Goal = api.WorkflowGoalMediaReady
	request.Intent.Descriptions = nil
	command, stage = planContinuationCommand(request, current, now)
	if command != nil || stage != "" {
		t.Fatalf("media-ready capture scheduled image hosting: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationPlannerAcceptsFinalizedProjectionSetBoundToPreflight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	preflightRef := api.TrackerPreflightAssessmentRef{ID: "preflight-final", Revision: 5}
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{
			ID:               "workflow-final",
			Revision:         7,
			TrackerPreflight: &preflightRef,
		},
		Release: &api.ReleaseSnapshot{ID: "release-final", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{}},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-final",
			Revision:         6,
			Preflight:        &preflightRef,
			InputFingerprint: testFingerprint(t, "projection-final"),
			Projections: []api.TrackerReleaseProjection{{
				TrackerID: "ALPHA",
				Readiness: api.ReadinessStatusReady,
				DupeReady: true,
			}},
		},
		Preflight: &api.TrackerPreflightAssessment{
			ID:            preflightRef.ID,
			Revision:      preflightRef.Revision,
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-initial", Revision: 3},
			ExpiresAt:     now.Add(time.Hour),
		},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-finalized-preflight",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}
	command, stage := planContinuationCommand(request, current, now)
	if _, ok := command.(CheckDuplicatesCommand); !ok || stage != "check-duplicates" {
		t.Fatalf("finalized preflight plan: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationPlannerIgnoresSemanticallyEmptyProjectionInstructions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-empty-instructions", Revision: 7},
		Release:  &api.ReleaseSnapshot{ID: "release-empty-instructions", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{
			Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-empty-instructions",
			Revision:         3,
			InputFingerprint: testFingerprint(t, "projection-empty-instructions"),
		},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-empty-instructions",
		Goal:           api.WorkflowGoalTrackersAssessed,
		Intent: api.WorkflowIntent{
			TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{
				"ALPHA": {},
				"BETA":  {Questionnaire: map[string]*string{}},
			},
		},
	}
	command, stage := planContinuationCommand(request, current, now)
	preflight, ok := command.(PreflightTrackersCommand)
	if !ok || stage != "preflight-trackers" {
		t.Fatalf("empty-instruction plan: stage=%q command=%#v", stage, command)
	}
	if preflight.Interaction != api.InteractionModeInteractive {
		t.Fatalf("preflight interaction = %q, want interactive default", preflight.Interaction)
	}
}

func TestContinuationPlannerReprojectsWhenExecutionModeChanges(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-mode", Revision: 7},
		Release:  &api.ReleaseSnapshot{ID: "release-mode", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{
			Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-mode",
			Revision:         3,
			ExecutionMode:    api.WorkflowExecutionModeNormal,
			InputFingerprint: testFingerprint(t, "projection-mode"),
		},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-debug-mode",
		Goal:           api.WorkflowGoalTrackersAssessed,
		Intent: api.WorkflowIntent{
			ExecutionMode:          api.WorkflowExecutionModeDebug,
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}

	command, stage := planContinuationCommand(request, current, now)
	projection, ok := command.(ProjectTrackersCommand)
	if !ok || stage != "project-trackers" || projection.ExecutionMode != api.WorkflowExecutionModeDebug {
		t.Fatalf("execution-mode reproject: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationPlannerReappliesPreflightForUnattendedManualActions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	preflightRef := api.TrackerPreflightAssessmentRef{ID: "preflight-action", Revision: 5}
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{
			ID:               "workflow-action",
			Revision:         7,
			TrackerPreflight: &preflightRef,
		},
		Release: &api.ReleaseSnapshot{ID: "release-action", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{
			Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-action",
			Revision:         6,
			Preflight:        &preflightRef,
			InputFingerprint: testFingerprint(t, "projection-action"),
		},
		Preflight: &api.TrackerPreflightAssessment{
			ID:            preflightRef.ID,
			Revision:      preflightRef.Revision,
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-initial", Revision: 3},
			ExpiresAt:     now.Add(time.Hour),
			Results: []api.TrackerPreflightResult{{
				TrackerID: "ALPHA",
				State:     api.TrackerPreflightStateActionRequired,
				RequiredActions: []api.RequiredAction{{
					Kind: legacyTrackerAuthActionKind,
				}},
			}},
		},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-unattended-action",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent: api.WorkflowIntent{
			Interaction:            api.InteractionModeUnattended,
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}
	if continuationGoalReached(current, request) {
		t.Fatal("unattended manual action incorrectly satisfied continuation goal")
	}
	command, stage := planContinuationCommand(request, current, now)
	preflight, ok := command.(PreflightTrackersCommand)
	if !ok || stage != "preflight-trackers" || preflight.Interaction != api.InteractionModeUnattended {
		t.Fatalf("unattended action plan: stage=%q command=%#v", stage, command)
	}
}

func TestUnattendedPreflightPolicySkipsOnlyManualTrackerLane(t *testing.T) {
	t.Parallel()

	assessment := api.TrackerPreflightAssessment{Results: []api.TrackerPreflightResult{
		{
			TrackerID: "ALPHA",
			State:     api.TrackerPreflightStateActionRequired,
			RequiredActions: []api.RequiredAction{{
				Kind: legacyTrackerTwoFactorActionKind,
			}},
		},
		{
			TrackerID: "BETA",
			State:     api.TrackerPreflightStateReady,
		},
	}}
	finalized := []api.TrackerReleaseProjection{
		{
			TrackerID:   "ALPHA",
			Readiness:   api.ReadinessStatusBlocked,
			DupeReady:   false,
			UploadReady: false,
			RequiredActions: []api.RequiredAction{{
				Kind: legacyTrackerTwoFactorActionKind,
			}},
		},
		{
			TrackerID:   "BETA",
			Readiness:   api.ReadinessStatusReady,
			DupeReady:   true,
			UploadReady: true,
		},
	}

	applyPreflightInteractionPolicy(api.InteractionModeUnattended, &assessment, finalized)

	if assessment.Results[0].State != api.TrackerPreflightStateFailed || len(assessment.Results[0].RequiredActions) != 0 ||
		len(assessment.Results[0].Failures) != 1 {
		t.Fatalf("unattended ALPHA result = %#v", assessment.Results[0])
	}
	if finalized[0].Readiness != api.ReadinessStatusIneligible || finalized[0].DupeReady || finalized[0].UploadReady ||
		len(finalized[0].RequiredActions) != 0 {
		t.Fatalf("unattended ALPHA projection = %#v", finalized[0])
	}
	if assessment.Results[1].State != api.TrackerPreflightStateReady || finalized[1].Readiness != api.ReadinessStatusReady ||
		!finalized[1].DupeReady || !finalized[1].UploadReady {
		t.Fatalf("unattended BETA lane changed = %#v/%#v", assessment.Results[1], finalized[1])
	}
}

func TestPreflightInteractionPolicyPreservesAuthBlockedLane(t *testing.T) {
	t.Parallel()

	for _, interaction := range []api.InteractionMode{
		api.InteractionModeInteractive,
		api.InteractionModeUnattended,
		api.InteractionModeUnattendedConfirm,
	} {
		t.Run(string(interaction), func(t *testing.T) {
			t.Parallel()
			assessment := api.TrackerPreflightAssessment{Results: []api.TrackerPreflightResult{{
				TrackerID: "ALPHA",
				State:     api.TrackerPreflightStateRetryable,
				Failures: []api.WorkflowFailure{{
					Failure: api.OperationFailure{
						Code:     api.OperationFailureTrackerAuthRequired,
						Recovery: api.OperationRecoveryAuthenticateTrackers,
					},
				}},
			}}}
			finalized := []api.TrackerReleaseProjection{{
				TrackerID:   "ALPHA",
				Readiness:   api.ReadinessStatusBlocked,
				DupeReady:   false,
				UploadReady: false,
			}}

			applyPreflightInteractionPolicy(interaction, &assessment, finalized)

			if assessment.Results[0].State != api.TrackerPreflightStateRetryable ||
				len(assessment.Results[0].RequiredActions) != 0 ||
				len(assessment.Results[0].Failures) != 1 ||
				assessment.Results[0].Failures[0].Failure.Code != api.OperationFailureTrackerAuthRequired ||
				finalized[0].Readiness != api.ReadinessStatusBlocked ||
				finalized[0].DupeReady ||
				finalized[0].UploadReady {
				t.Fatalf("interaction %q changed auth lane: %#v/%#v", interaction, assessment.Results[0], finalized[0])
			}
		})
	}
}

func TestContinuationPlannerReconcilesChangedDuplicateDecision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Dupes.Results[0].Decision = api.DupeDecisionAccepted
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-change-dupe",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
			DuplicateDecisions: map[api.TrackerID]api.DupeDecision{
				"ALPHA": api.DupeDecisionIgnored,
			},
		},
	}
	if continuationGoalReached(current, request) {
		t.Fatal("changed duplicate decision incorrectly satisfied continuation goal")
	}
	command, stage := planContinuationCommand(request, current, now)
	decision, ok := command.(DecideDuplicatesCommand)
	if !ok || stage != "decide-duplicates" || decision.Decisions["ALPHA"] != api.DupeDecisionIgnored {
		t.Fatalf("changed duplicate plan: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationPlannerRunsRequestedSecondDuplicateCheck(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Dupes.CheckOrdinal = 1
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-double-dupe",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
			DuplicateCheckCount:    2,
		},
	}
	if continuationGoalReached(current, request) {
		t.Fatal("first duplicate assessment incorrectly satisfied requested second check")
	}
	command, stage := planContinuationCommand(request, current, now)
	check, ok := command.(CheckDuplicatesCommand)
	if !ok || stage != "check-duplicates" || check.CheckOrdinal != 2 {
		t.Fatalf("second duplicate check plan: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationPlannerForceReprepareIsSatisfiedByRetainedInputLineage(t *testing.T) {
	t.Parallel()

	input := api.PrepareInput{
		SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		Instructions: api.ReleaseFactInstructions{
			SourceLookup: "Example Release 2026",
		},
		Force: true,
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(input)
	if err != nil {
		t.Fatalf("fingerprint preparation input: %v", err)
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-reprepare",
		Goal:           api.WorkflowGoalPrepared,
		Intent:         api.WorkflowIntent{Preparation: &input},
	}
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-reprepare", Revision: 4},
		Release: &api.ReleaseSnapshot{
			ID:                     "release-reprepare",
			Revision:               4,
			PreparationFingerprint: fingerprint,
		},
	}
	if !continuationGoalReached(current, request) {
		t.Fatal("retained exact force-preparation input did not satisfy prepared goal")
	}
	command, stage := planContinuationCommand(request, current, time.Now())
	if command != nil || stage != "" {
		t.Fatalf("satisfied preparation planned stage=%q command=%#v", stage, command)
	}

	current.Release.PreparationFingerprint = ""
	command, stage = planContinuationCommand(request, current, time.Now())
	if _, ok := command.(ResetReleaseCommand); !ok || stage != "reprepare" {
		t.Fatalf("stale preparation planned stage=%q command=%#v", stage, command)
	}
}

func readyContinuationPlannerResult(t *testing.T, now time.Time) CommandResult {
	t.Helper()
	return CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-ready", Revision: 7},
		Release:  &api.ReleaseSnapshot{ID: "release-ready", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{}},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-ready",
			Revision:         3,
			InputFingerprint: testFingerprint(t, "projection-ready"),
			Projections: []api.TrackerReleaseProjection{{
				TrackerID:   "ALPHA",
				Readiness:   api.ReadinessStatusReady,
				DupeReady:   true,
				UploadReady: true,
			}},
		},
		Preflight: &api.TrackerPreflightAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-ready", Revision: 3},
			ExpiresAt:     now.Add(time.Hour),
		},
		Dupes: &api.DupeAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-ready", Revision: 3},
			Status:        api.StageStatusCompleted,
			ExpiresAt:     now.Add(time.Hour),
			Results: []api.TrackerDupeAssessment{{
				TrackerID: "ALPHA",
				Status:    api.StageStatusCompleted,
				Decision:  api.DupeDecisionNoMatch,
			}},
		},
	}
}

func TestContinuationPlannerAdvancesRunnableSiblingPastPendingDupe(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-partial", Revision: 7},
		Release:  &api.ReleaseSnapshot{ID: "release-partial", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{}},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-partial",
			Revision:         3,
			Status:           api.StageStatusReady,
			InputFingerprint: testFingerprint(t, "projection-partial"),
			Projections: []api.TrackerReleaseProjection{
				{
					TrackerID:   "ALPHA",
					Readiness:   api.ReadinessStatusReady,
					DupeReady:   true,
					UploadReady: true,
				},
				{
					TrackerID:   "BETA",
					Readiness:   api.ReadinessStatusReady,
					DupeReady:   true,
					UploadReady: true,
				},
			},
		},
		Preflight: &api.TrackerPreflightAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-partial", Revision: 3},
			Status:        api.StageStatusReady,
			ExpiresAt:     now.Add(time.Hour),
		},
		Dupes: &api.DupeAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-partial", Revision: 3},
			Status:        api.StageStatusBlocked,
			ExpiresAt:     now.Add(time.Hour),
			Results: []api.TrackerDupeAssessment{
				{
					TrackerID: "ALPHA",
					Status:    api.StageStatusCompleted,
					Decision:  api.DupeDecisionNoMatch,
				},
				{
					TrackerID: "BETA",
					Status:    api.StageStatusBlocked,
					Decision:  api.DupeDecisionPending,
				},
			},
		},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-partial",
		Goal:           api.WorkflowGoalMediaReady,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA", "BETA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
			Media:                  &api.MediaCaptureInstructions{},
		},
	}
	command, stage := planContinuationCommand(request, current, now)
	if _, ok := command.(CaptureMediaCommand); !ok || stage != "capture-media" {
		t.Fatalf("partial-lane plan: stage=%q command=%#v", stage, command)
	}
	action := api.RequiredAction{TrackerID: "BETA"}
	if continuationActionBlocksAllLanes(current, action) {
		t.Fatal("pending BETA action blocked runnable ALPHA lane")
	}
	action.Kind = api.RequiredActionReviewDuplicates
	if !continuationActionBlocksAllLanesForMode(current, action, TrackerDecisionModePostDupeGate) {
		t.Fatal("gated duplicate review did not block all runnable lanes")
	}
	if continuationActionBlocksAllLanesForMode(current, action, TrackerDecisionModeWebUIControls) {
		t.Fatal("WebUI duplicate review blocked runnable sibling lane")
	}
	waivableFingerprint := testFingerprint(t, "planner-waivable-rule")
	ruleAction := api.RequiredAction{
		ID:        "authorize-beta-rules",
		Kind:      api.RequiredActionAuthorizeRules,
		TrackerID: "BETA",
		Status:    api.RequiredActionStatusPending,
	}
	current.Projections.Projections[1].WaivableRuleFingerprint = waivableFingerprint
	current.Projections.Projections[1].RequiredActions = []api.RequiredAction{ruleAction}
	if !continuationActionBlocksAllLanesForMode(current, ruleAction, TrackerDecisionModePostDupeGate) {
		t.Fatal("CLI tracker rule prompt did not retain its explicit gate")
	}
	if continuationActionBlocksAllLanesForMode(current, ruleAction, TrackerDecisionModeWebUIControls) {
		t.Fatal("WebUI tracker rule warning blocked runnable workflow lanes")
	}
	current.Projections.Projections = current.Projections.Projections[1:]
	if continuationActionBlocksAllLanesForMode(current, ruleAction, TrackerDecisionModeWebUIControls) {
		t.Fatal("WebUI tracker rule warning blocked its dupe-card workflow")
	}
}

func TestContinuationPlannerPreservesAuthBlockedLaneWhileCheckingReadySibling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Selection.TrackerIDs = []api.TrackerID{"ALPHA", "BETA"}
	current.Projections.Projections = append(current.Projections.Projections, api.TrackerReleaseProjection{
		TrackerID:   "BETA",
		Readiness:   api.ReadinessStatusBlocked,
		DupeReady:   false,
		UploadReady: false,
	})
	current.Preflight.Status = api.StageStatusReady
	current.Preflight.Results = []api.TrackerPreflightResult{
		{
			TrackerID: "ALPHA",
			State:     api.TrackerPreflightStateReady,
		},
		{
			TrackerID: "BETA",
			State:     api.TrackerPreflightStateRetryable,
			Failures: []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:     api.OperationFailureTrackerAuthRequired,
					Recovery: api.OperationRecoveryAuthenticateTrackers,
				},
			}},
		},
	}
	current.Dupes = nil
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-auth-blocked-sibling",
		Goal:           api.WorkflowGoalUploaded,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA", "BETA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}

	command, stage := planContinuationCommand(request, current, now)
	if _, ok := command.(CheckDuplicatesCommand); !ok || stage != "check-duplicates" {
		t.Fatalf("mixed auth plan: stage=%q command=%#v", stage, command)
	}
	if !slices.Equal(current.Selection.TrackerIDs, []api.TrackerID{"ALPHA", "BETA"}) {
		t.Fatalf("selection changed: %v", current.Selection.TrackerIDs)
	}
}

func TestContinuationPlannerStopsWhenEveryTrackerIsAuthBlocked(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Projections.Status = api.StageStatusFailed
	current.Projections.Projections[0].Readiness = api.ReadinessStatusBlocked
	current.Projections.Projections[0].DupeReady = false
	current.Projections.Projections[0].UploadReady = false
	current.Preflight.Status = api.StageStatusFailed
	current.Preflight.Results = []api.TrackerPreflightResult{{
		TrackerID: "ALPHA",
		State:     api.TrackerPreflightStateRetryable,
		Failures: []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:     api.OperationFailureTrackerAuthRequired,
				Recovery: api.OperationRecoveryAuthenticateTrackers,
			},
		}},
	}}
	current.Dupes = nil
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-all-auth-blocked",
		Goal:           api.WorkflowGoalUploaded,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}

	command, stage := planContinuationCommand(request, current, now)
	if command != nil || stage != "no-eligible-trackers" {
		t.Fatalf("all auth blocked plan: stage=%q command=%#v", stage, command)
	}
}

func TestDuplicateReviewBlocksWhenOtherLaneNeedsPreflightAction(t *testing.T) {
	t.Parallel()

	current := CommandResult{
		Projections: &api.TrackerReleaseProjectionSet{
			Projections: []api.TrackerReleaseProjection{
				{
					TrackerID:   "ALPHA",
					Readiness:   api.ReadinessStatusReady,
					DupeReady:   true,
					UploadReady: true,
				},
				{
					TrackerID:   "BETA",
					Readiness:   api.ReadinessStatusBlocked,
					DupeReady:   false,
					UploadReady: false,
				},
			},
		},
		Preflight: &api.TrackerPreflightAssessment{
			Results: []api.TrackerPreflightResult{
				{
					TrackerID: "ALPHA",
					State:     api.TrackerPreflightStateReady,
				},
				{
					TrackerID: "BETA",
					State:     api.TrackerPreflightStateActionRequired,
					RequiredActions: []api.RequiredAction{{
						Kind: legacyTrackerAuthActionKind,
					}},
				},
			},
		},
		Dupes: &api.DupeAssessment{
			Results: []api.TrackerDupeAssessment{
				{
					TrackerID: "ALPHA",
					Status:    api.StageStatusCompleted,
					Decision:  api.DupeDecisionAccepted,
				},
				{
					TrackerID: "BETA",
					Status:    api.StageStatusSkipped,
					Decision:  api.DupeDecisionSkipped,
				},
			},
		},
	}
	action := api.RequiredAction{
		Kind:      api.RequiredActionReviewDuplicates,
		TrackerID: "ALPHA",
	}
	if !continuationActionBlocksAllLanes(current, action) {
		t.Fatal("duplicate review did not block when every sibling lane was unavailable")
	}
	if dupesAllowContinuation(current.Projections, current.Dupes) {
		t.Fatal("blocked projection's skipped duplicate result allowed continuation")
	}
}

func TestContinuationPlannerRecapturesFailedMenuMediaWithoutExactOutcome(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Media = &api.MediaArtifactSet{
		ID:       "media-failed",
		Revision: 8,
		Status:   api.StageStatusFailed,
		Artifacts: []api.MediaArtifact{{
			ID:       "menu-retained",
			Kind:     api.MediaArtifactDVDMenu,
			Selected: true,
		}},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-failed-media",
		Goal:           api.WorkflowGoalMediaReady,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
			Media: &api.MediaCaptureInstructions{
				Purpose:         api.ScreenshotPurposeMenu,
				CaptureDVDMenus: true,
			},
		},
	}
	command, stage := planContinuationCommand(request, current, now)
	if _, ok := command.(CaptureMediaCommand); !ok || stage != "capture-media" {
		t.Fatalf("failed retained media plan: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationMenuFallbackRequiresPriorAutomaticCapture(t *testing.T) {
	t.Parallel()

	current := readyContinuationPlannerResult(t, time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC))
	current.Media = &api.MediaArtifactSet{
		Status: api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{{
			ID:       "menu-retained",
			Kind:     api.MediaArtifactDVDMenu,
			Selected: true,
			Source:   api.ScreenshotSelectionSourceDVDMenu,
		}},
	}
	desired := &api.MediaCaptureInstructions{
		Purpose:         api.ScreenshotPurposeMenu,
		CaptureDVDMenus: true,
		MaxDVDMenuItems: 6,
	}
	if !continuationMediaCaptureSatisfied(current, desired) {
		t.Fatal("completed automatic menu capture was not retained below its cap")
	}
	request := api.ContinueReleaseWorkflowRequest{
		Goal:   api.WorkflowGoalMediaReady,
		Intent: api.WorkflowIntent{Media: desired},
	}
	if !continuationGoalReached(current, request) {
		t.Fatal("completed automatic menu capture did not satisfy media goal")
	}

	current.Media.Artifacts[0].Source = api.ScreenshotSelectionSourceMenu
	if continuationMediaCaptureSatisfied(current, desired) {
		t.Fatal("manual menu incorrectly satisfied automatic menu capture intent")
	}
	if continuationGoalReached(current, request) {
		t.Fatal("manual menu incorrectly satisfied automatic menu capture goal")
	}
}

func TestContinuationPlannerRecapturesRequestedMediaAfterArtifactsDeleted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Projections.ReleaseRef = api.ReleaseRef{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP", Generation: 1}
	current.Media = &api.MediaArtifactSet{
		ID:                      "media-deleted",
		Revision:                8,
		Status:                  api.StageStatusBlocked,
		CaptureFingerprint:      testFingerprint(t, "deleted-media"),
		RequirementsFingerprint: testFingerprint(t, "media-requirements"),
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-recapture-media",
		Goal:           api.WorkflowGoalMediaReady,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
			Media: &api.MediaCaptureInstructions{
				ScreenshotCount: 1,
				Purpose:         api.ScreenshotPurposeFinal,
			},
		},
	}
	command, stage := planContinuationCommand(request, current, now)
	if _, ok := command.(CaptureMediaCommand); !ok || stage != "capture-media" {
		t.Fatalf("deleted media recapture plan: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationPlannerCapturesEachNewExplicitScreenshotIndex(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := readyContinuationPlannerResult(t, now)
	current.Media = &api.MediaArtifactSet{
		Status: api.StageStatusBlocked,
		Artifacts: []api.MediaArtifact{{
			ID:       "screen-3",
			Kind:     api.MediaArtifactScreenshot,
			Index:    3,
			Selected: true,
		}},
		RequiredActions: []api.RequiredAction{{
			ID:     "media-required",
			Kind:   api.RequiredActionProvideTrackerInput,
			Status: api.RequiredActionStatusPending,
		}},
	}
	request := api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "continue-capture-explicit",
		Goal:           api.WorkflowGoalMediaReady,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
			Media: &api.MediaCaptureInstructions{
				Purpose: api.ScreenshotPurposeFinal,
				Selections: []api.ScreenshotSelection{{
					Index: 4,
				}},
			},
		},
	}

	command, stage := planContinuationCommand(request, current, now)
	if _, ok := command.(CaptureMediaCommand); !ok || stage != "capture-media" {
		t.Fatalf("new explicit screenshot plan: stage=%q command=%#v", stage, command)
	}

	request.Intent.Media.Selections[0].Index = 3
	command, stage = planContinuationCommand(request, current, now)
	if command != nil || stage != "capture-media" {
		t.Fatalf("retained explicit screenshot plan: stage=%q command=%#v", stage, command)
	}
}

func TestContinuationMediaIntentCanResolveItsPendingGlobalAction(t *testing.T) {
	t.Parallel()

	action := api.RequiredAction{
		ID:     "action-media",
		Kind:   api.RequiredActionProvideTrackerInput,
		Status: api.RequiredActionStatusPending,
	}
	current := CommandResult{
		Media: &api.MediaArtifactSet{RequiredActions: []api.RequiredAction{action}},
	}
	intent := api.WorkflowIntent{
		Media: &api.MediaCaptureInstructions{
			ScreenshotCount: 1,
			Purpose:         api.ScreenshotPurposeFinal,
		},
	}

	if !continuationIntentResolvesAction(intent, current, action) {
		t.Fatal("exact media intent did not resolve its pending media action")
	}
	unrelated := action
	unrelated.ID = "action-tracker"
	if continuationIntentResolvesAction(intent, current, unrelated) {
		t.Fatal("media intent resolved an unrelated global tracker action")
	}
}

func TestStrictUnattendedSkipsTrackerReleaseNameConfirmation(t *testing.T) {
	t.Parallel()

	action := api.RequiredAction{
		Kind:      api.RequiredActionProvideTrackerInput,
		TrackerID: "AR",
	}
	if !continuationUnattendedSkipsTrackerAction(api.WorkflowIntent{
		Interaction: api.InteractionModeUnattended,
	}, action) {
		t.Fatal("strict unattended mode did not skip tracker-scoped release-name confirmation")
	}
	if continuationUnattendedSkipsTrackerAction(api.WorkflowIntent{
		Interaction: api.InteractionModeUnattendedConfirm,
	}, action) {
		t.Fatal("unattended-confirm mode skipped interactive release-name confirmation")
	}
}
