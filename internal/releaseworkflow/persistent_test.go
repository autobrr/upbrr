// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPersistentWorkflowRestartRetriesAuthBlockedProjectionOnce(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "auth-restart.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}

	var authReady atomic.Bool
	var projectionBuilds atomic.Int32
	var preflightBuilds atomic.Int32
	projector := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		_ map[api.TrackerID]api.WorkflowFingerprint,
		executionMode api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		projectionBuilds.Add(1)
		projections := make([]api.TrackerReleaseProjection, 0, len(trackerIDs))
		for _, trackerID := range trackerIDs {
			projections = append(projections, testProjection(t, trackerID, "Example.Release.2026."+string(trackerID)+"-GRP"))
		}
		return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: slices.Clone(trackerIDs)}, api.TrackerReleaseProjectionSet{
			InputFingerprint:  testFingerprint(t, "auth-restart-projection"),
			PolicyFingerprint: testFingerprint(t, "auth-restart-policy"),
			ExecutionMode:     executionMode,
			Projections:       projections,
			Status:            api.StageStatusReady,
		}, nil
	})
	preflight := trackerPreflightBuilderFunc(func(
		_ context.Context,
		_ api.UploadSubject,
		_ api.TrackerCatalogSnapshot,
		_ api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		preflightBuilds.Add(1)
		results := make([]api.TrackerPreflightResult, 0, len(initial.Projections))
		finalized := slices.Clone(initial.Projections)
		for index, projection := range initial.Projections {
			fingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(projection)
			if fingerprintErr != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint auth restart projection: %w", fingerprintErr)
			}
			result := api.TrackerPreflightResult{
				TrackerID:             projection.TrackerID,
				State:                 api.TrackerPreflightStateReady,
				AuthReady:             true,
				ClaimsReady:           true,
				BannedGroupsReady:     true,
				RemoteMetadataReady:   true,
				ConfigFingerprint:     projection.ConfigFingerprint,
				ProjectionFingerprint: fingerprint,
				AssessedAt:            now,
				FreshUntil:            now.Add(time.Hour),
			}
			if projection.TrackerID == "BETA" && !authReady.Load() {
				result.State = api.TrackerPreflightStateRetryable
				result.AuthReady = false
				result.Failures = []api.WorkflowFailure{{
					Failure: api.OperationFailure{
						Code:      api.OperationFailureTrackerAuthRequired,
						Operation: api.OperationKindDuplicateCheck,
						Message:   "Tracker authentication is not ready for this attempt.",
						Recovery:  api.OperationRecoveryAuthenticateTrackers,
					},
					TrackerID: projection.TrackerID,
				}}
				finalized[index].Readiness = api.ReadinessStatusBlocked
				finalized[index].DupeReady = false
				finalized[index].UploadReady = false
				finalized[index].Failures = slices.Clone(result.Failures)
			}
			results = append(results, result)
		}
		return api.TrackerPreflightAssessment{
			InputFingerprint: testFingerprint(t, "auth-restart-preflight"),
			Results:          results,
			ExpiresAt:        now.Add(time.Hour),
		}, finalized, nil
	})
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-auth-a"),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(preflight),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	result := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-auth-restart"})
	result = executeCommand(t, moduleA, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP"),
		},
	})
	result = executeCommand(t, moduleA, ProjectTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
		Instructions:     map[api.TrackerID]api.TrackerProjectionInstructions{},
	})
	result = executeCommand(t, moduleA, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	if result.Preflight == nil || result.Projections == nil ||
		result.Preflight.Results[1].State != api.TrackerPreflightStateRetryable ||
		result.Projections.Projections[1].Readiness != api.ReadinessStatusBlocked {
		_ = repoA.Close()
		t.Fatalf("epoch A auth-blocked result = %#v", result)
	}
	oldProjectionID := result.Projections.ID
	oldPreflightID := result.Preflight.ID
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	authReady.Store(true)
	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Minute)}),
		WithIDGenerator(&sequenceIDGenerator{next: 100}),
		WithProcessEpoch("epoch-auth-b"),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(preflight),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	recovered, err := moduleB.Current(context.Background(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatalf("recover auth-blocked workflow: %v", err)
	}
	if recovered.Projections != nil || recovered.Preflight != nil ||
		recovered.Selection == nil || recovered.ProjectionInstructions == nil ||
		!slices.Equal(recovered.Selection.TrackerIDs, []api.TrackerID{"ALPHA", "BETA"}) {
		t.Fatalf("recovered auth authority = %#v", recovered)
	}
	recoveredAgain, err := moduleB.Current(context.Background(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatalf("repeat same-epoch recovery: %v", err)
	}
	if recoveredAgain.Workflow.Revision != recovered.Workflow.Revision {
		t.Fatalf("same-epoch recovery revision = %d, want %d", recoveredAgain.Workflow.Revision, recovered.Workflow.Revision)
	}

	request := api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       recovered.Workflow.ID,
			ExpectedRevision: recovered.Workflow.Revision,
		},
		IdempotencyKey: "continue-auth-restart-project",
		Goal:           api.WorkflowGoalTrackersAssessed,
		Intent: api.WorkflowIntent{
			TrackerIDs:             []api.TrackerID{"ALPHA", "BETA"},
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		},
	}
	projecting, err := moduleB.Continue(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("continue auth restart projection: %v", err)
	}
	if projecting.Operation == nil {
		t.Fatalf("auth restart projection operation = %#v", projecting)
	}
	waitForWorkflowOperation(t, moduleB, recovered.Workflow.ID, projecting.Operation.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	projected, err := moduleB.Current(context.Background(), testOwnerID, recovered.Workflow.ID)
	if err != nil {
		t.Fatalf("load fresh projection: %v", err)
	}
	request.Authority.ExpectedRevision = projected.Workflow.Revision
	request.IdempotencyKey = "continue-auth-restart-preflight"
	preflighting, err := moduleB.Continue(context.Background(), testOwnerID, request)
	if err != nil {
		t.Fatalf("continue auth restart preflight: %v", err)
	}
	if preflighting.Operation == nil {
		t.Fatalf("auth restart preflight operation = %#v", preflighting)
	}
	waitForWorkflowOperation(t, moduleB, recovered.Workflow.ID, preflighting.Operation.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	fresh, err := moduleB.Current(context.Background(), testOwnerID, recovered.Workflow.ID)
	if err != nil {
		t.Fatalf("load fresh preflight: %v", err)
	}
	if fresh.Preflight == nil || fresh.Projections == nil ||
		len(fresh.Preflight.Results) != 2 ||
		fresh.Preflight.Results[1].State != api.TrackerPreflightStateReady ||
		fresh.Projections.Projections[1].Readiness != api.ReadinessStatusReady {
		t.Fatalf("fresh auth projection = %#v", fresh)
	}
	if projectionBuilds.Load() != 2 || preflightBuilds.Load() != 2 {
		t.Fatalf("restart build counts: projections=%d preflights=%d", projectionBuilds.Load(), preflightBuilds.Load())
	}
	retained, err := persistentB.Load(context.Background(), testOwnerID, recovered.Workflow.ID)
	if err != nil {
		t.Fatalf("load retained auth history: %v", err)
	}
	if _, ok := retained.Projections[oldProjectionID]; !ok {
		t.Fatalf("old auth-blocked projection %s was not retained", oldProjectionID)
	}
	if _, ok := retained.Preflights[oldPreflightID]; !ok {
		t.Fatalf("old auth-blocked preflight %s was not retained", oldPreflightID)
	}
}

func TestPersistentRestartHandlesLegacyAuthStateSafely(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "legacy-auth-restart.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	projector := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		_ map[api.TrackerID]api.WorkflowFingerprint,
		executionMode api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		projections := make([]api.TrackerReleaseProjection, 0, len(trackerIDs))
		for _, trackerID := range trackerIDs {
			projections = append(projections, testProjection(t, trackerID, "Example.Release.2026."+string(trackerID)+"-GRP"))
		}
		return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: slices.Clone(trackerIDs)}, api.TrackerReleaseProjectionSet{
			InputFingerprint:  testFingerprint(t, "legacy-auth-projection"),
			PolicyFingerprint: testFingerprint(t, "legacy-auth-policy"),
			ExecutionMode:     executionMode,
			Projections:       projections,
			Status:            api.StageStatusReady,
		}, nil
	})
	preflight := trackerPreflightBuilderFunc(func(
		_ context.Context,
		_ api.UploadSubject,
		_ api.TrackerCatalogSnapshot,
		_ api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		projection := initial.Projections[0]
		fingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(projection)
		if fingerprintErr != nil {
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint legacy auth projection: %w", fingerprintErr)
		}
		action := api.RequiredAction{
			Kind:   legacyTrackerAuthActionKind,
			Prompt: "Authenticate this tracker.",
		}
		result := api.TrackerPreflightResult{
			TrackerID:             projection.TrackerID,
			State:                 api.TrackerPreflightStateActionRequired,
			AuthReady:             false,
			ClaimsReady:           true,
			BannedGroupsReady:     true,
			RemoteMetadataReady:   true,
			ConfigFingerprint:     projection.ConfigFingerprint,
			ProjectionFingerprint: fingerprint,
			RequiredActions:       []api.RequiredAction{action},
			AssessedAt:            now,
			FreshUntil:            now.Add(time.Hour),
		}
		finalized := slices.Clone(initial.Projections)
		finalized[0].Readiness = api.ReadinessStatusBlocked
		finalized[0].DupeReady = false
		finalized[0].UploadReady = false
		finalized[0].RequiredActions = []api.RequiredAction{action}
		return api.TrackerPreflightAssessment{
			InputFingerprint: testFingerprint(t, "legacy-auth-preflight"),
			Results:          []api.TrackerPreflightResult{result},
			ExpiresAt:        now.Add(time.Hour),
		}, finalized, nil
	})
	now := time.Date(2026, time.July, 27, 13, 0, 0, 0, time.UTC)
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-legacy-auth-a"),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(preflight),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	createLegacyAuthWorkflow := func(workflowID api.WorkflowID) CommandResult {
		t.Helper()
		result := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: workflowID})
		result = executeCommand(t, moduleA, PrepareReleaseCommand{
			WorkflowID:       result.Workflow.ID,
			ExpectedRevision: result.Workflow.Revision,
			Input: api.PrepareInput{
				SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP"),
			},
		})
		result = executeCommand(t, moduleA, ProjectTrackersCommand{
			WorkflowID:       result.Workflow.ID,
			ExpectedRevision: result.Workflow.Revision,
			TrackerIDs:       []api.TrackerID{"ALPHA"},
			Instructions:     map[api.TrackerID]api.TrackerProjectionInstructions{},
		})
		result = executeCommand(t, moduleA, PreflightTrackersCommand{
			WorkflowID:       result.Workflow.ID,
			ExpectedRevision: result.Workflow.Revision,
			Interaction:      api.InteractionModeInteractive,
		})
		if result.Workflow.Status != api.WorkflowStatusBlocked ||
			len(result.Workflow.RequiredActions) != 1 ||
			result.Workflow.RequiredActions[0].Kind != legacyTrackerAuthActionKind {
			t.Fatalf("legacy auth workflow = %#v", result)
		}
		return result
	}
	safe := createLegacyAuthWorkflow("workflow-legacy-auth-safe")
	ambiguous := createLegacyAuthWorkflow("workflow-legacy-auth-ambiguous")
	ambiguousState, err := persistentA.Load(context.Background(), testOwnerID, ambiguous.Workflow.ID)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("load ambiguous legacy workflow: %v", err)
	}
	expectedRevision := ambiguousState.Workflow.Revision
	ambiguousState.Workflow.Revision++
	ambiguousState.Workflow.UpdatedAt = now.Add(time.Second)
	for index := range ambiguousState.Workflow.RequiredActions {
		ambiguousState.Workflow.RequiredActions[index].WorkflowRevision = ambiguousState.Workflow.Revision
	}
	ambiguousState.Composite = &compositeUploadSession{
		Version:        1,
		Goal:           api.WorkflowGoalUploaded,
		RemoveTrackers: []api.TrackerID{"ALPHA"},
	}
	if err := ambiguousState.Workflow.Validate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("validate ambiguous legacy workflow: %v", err)
	}
	if err := persistentA.Save(context.Background(), testOwnerID, expectedRevision, ambiguousState); err != nil {
		_ = repoA.Close()
		t.Fatalf("save ambiguous legacy workflow: %v", err)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Minute)}),
		WithIDGenerator(&sequenceIDGenerator{next: 100}),
		WithProcessEpoch("epoch-legacy-auth-b"),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(preflight),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	recoveredSafe, err := moduleB.Current(context.Background(), testOwnerID, safe.Workflow.ID)
	if err != nil {
		t.Fatalf("recover safe legacy auth workflow: %v", err)
	}
	if recoveredSafe.Workflow.Status != api.WorkflowStatusActive ||
		len(recoveredSafe.Workflow.RequiredActions) != 0 ||
		recoveredSafe.Projections != nil ||
		recoveredSafe.Preflight != nil ||
		recoveredSafe.Selection == nil ||
		!slices.Equal(recoveredSafe.Selection.TrackerIDs, []api.TrackerID{"ALPHA"}) {
		t.Fatalf("safe legacy auth recovery = %#v", recoveredSafe)
	}
	recoveredAgain, err := moduleB.Current(context.Background(), testOwnerID, safe.Workflow.ID)
	if err != nil {
		t.Fatalf("repeat safe legacy auth recovery: %v", err)
	}
	if recoveredAgain.Workflow.Revision != recoveredSafe.Workflow.Revision {
		t.Fatalf("same-epoch safe recovery revision = %d, want %d", recoveredAgain.Workflow.Revision, recoveredSafe.Workflow.Revision)
	}

	recoveredAmbiguous, err := moduleB.Current(context.Background(), testOwnerID, ambiguous.Workflow.ID)
	if err != nil {
		t.Fatalf("recover ambiguous legacy auth workflow: %v", err)
	}
	if recoveredAmbiguous.Workflow.Status != api.WorkflowStatusFailed ||
		len(recoveredAmbiguous.Workflow.RequiredActions) != 0 ||
		len(recoveredAmbiguous.Workflow.Failures) != 1 ||
		!strings.Contains(recoveredAmbiguous.Workflow.Failures[0].Failure.Message, "fresh upload workflow") {
		t.Fatalf("ambiguous legacy auth recovery = %#v", recoveredAmbiguous)
	}
}

func TestPersistentWorkflowRestartPreservesTrackerApproval(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "tracker-approval.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-tracker-approval-restart")
	approval := api.TrackerApprovalSnapshot{
		ID:                  "tracker-approval-restart",
		WorkflowID:          workflowID,
		Revision:            8,
		Release:             api.ReleaseSnapshotRef{ID: "release-restart", Revision: 2},
		Selection:           api.TrackerSelectionRef{ID: "selection-restart", Revision: 3},
		ProjectionSet:       api.TrackerReleaseProjectionSetRef{ID: "projections-restart", Revision: 4},
		Preflight:           api.TrackerPreflightAssessmentRef{ID: "preflight-restart", Revision: 5},
		Dupes:               api.DupeAssessmentRef{ID: "dupes-restart", Revision: 6},
		CandidateTrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
		ApprovedTrackerIDs:  []api.TrackerID{"BETA"},
		InputFingerprint:    testFingerprint(t, "tracker-approval-restart"),
		CreatedAt:           now,
	}
	if err := approval.Validate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("validate tracker approval: %v", err)
	}
	approvalRef := api.TrackerApprovalSnapshotRef{ID: approval.ID, Revision: approval.Revision}
	state := State{
		OwnerID:             testOwnerID,
		TrackerDecisionMode: TrackerDecisionModePostDupeGate,
		Workflow: api.ReleaseWorkflow{
			ID:              workflowID,
			Revision:        approval.Revision,
			TrackerApproval: &approvalRef,
			Status:          api.WorkflowStatusActive,
			CreatedAt:       now.Add(-time.Minute),
			UpdatedAt:       now,
		},
		TrackerApprovals: map[api.TrackerApprovalSnapshotID]api.TrackerApprovalSnapshot{
			approval.ID: approval,
		},
	}
	if _, _, err := persistentA.Create(
		context.Background(),
		testOwnerID,
		"create-tracker-approval-restart",
		testFingerprint(t, "create-tracker-approval-restart"),
		state,
	); err != nil {
		_ = repoA.Close()
		t.Fatalf("create workflow with tracker approval: %v", err)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	loaded, err := persistentB.Load(context.Background(), testOwnerID, workflowID)
	if err != nil {
		t.Fatalf("load restarted workflow: %v", err)
	}
	if loaded.TrackerDecisionMode != TrackerDecisionModePostDupeGate {
		t.Fatalf("restarted tracker decision mode = %q", loaded.TrackerDecisionMode)
	}
	if loaded.Workflow.TrackerApproval == nil || *loaded.Workflow.TrackerApproval != approvalRef {
		t.Fatalf("restarted workflow tracker approval = %#v", loaded.Workflow.TrackerApproval)
	}
	retained, ok := loaded.TrackerApprovals[approval.ID]
	if !ok {
		t.Fatalf("restarted tracker approval %s is missing", approval.ID)
	}
	if retained.InputFingerprint != approval.InputFingerprint ||
		len(retained.CandidateTrackerIDs) != 2 ||
		retained.CandidateTrackerIDs[0] != "ALPHA" ||
		retained.CandidateTrackerIDs[1] != "BETA" ||
		len(retained.ApprovedTrackerIDs) != 1 ||
		retained.ApprovedTrackerIDs[0] != "BETA" {
		t.Fatalf("restarted tracker approval = %#v", retained)
	}
}

func TestPersistentWorkflowRestartPreservesSafePreparedRelease(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "release-workflows.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	privateA := NewMemoryPrivateResourceStore()
	moduleA, err := New(
		persistentA,
		privateA,
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{
		WorkflowID:     "workflow-restart",
		IdempotencyKey: "create-restart",
	})
	prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		},
	})

	state, err := persistentA.Load(context.Background(), testOwnerID, prepared.Workflow.ID)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("load prepared state: %v", err)
	}
	state.Workflow.Revision = 3
	state.Workflow.Status = api.WorkflowStatusActive
	state.Workflow.UpdatedAt = now.Add(time.Minute)
	operationID := api.WorkflowOperationID("operation-before-restart")
	state.Operations[operationID] = api.WorkflowOperationStatus{
		ID:         operationID,
		WorkflowID: state.Workflow.ID,
		Revision:   3,
		Operation:  api.OperationKindUploadExecute,
		Status:     api.StageStatusRunning,
		Progress:   50,
		StartedAt:  now,
	}
	if err := persistentA.Save(context.Background(), testOwnerID, 2, state); err != nil {
		_ = repoA.Close()
		t.Fatalf("save reviewed state: %v", err)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(2 * time.Minute)}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}

	current, err := moduleB.Current(context.Background(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatalf("load restarted workflow: %v", err)
	}
	if current.Workflow.Revision != 4 || current.Workflow.Release == nil || current.Workflow.DryRun != nil || current.Workflow.UploadResult != nil {
		t.Fatalf("restarted workflow refs = %#v", current.Workflow)
	}
	if current.Workflow.Status != api.WorkflowStatusActive || len(current.Workflow.RequiredActions) != 0 {
		t.Fatalf("restarted workflow action = %#v", current.Workflow)
	}
	retained, err := persistentB.Load(context.Background(), testOwnerID, state.Workflow.ID)
	if err != nil {
		t.Fatalf("load recovered durable state: %v", err)
	}
	if len(retained.Releases) != 1 {
		t.Fatalf("safe release audit snapshot was removed: releases=%d", len(retained.Releases))
	}
	operation, err := moduleB.Operation(context.Background(), testOwnerID, state.Workflow.ID, operationID)
	if err != nil {
		t.Fatalf("load interrupted operation: %v", err)
	}
	if operation.Status != api.StageStatusInterrupted || operation.CompletedAt == nil || len(operation.Failures) != 1 {
		t.Fatalf("interrupted operation = %#v", operation)
	}
	if _, err := moduleB.Workflow(context.Background(), "different-owner", state.Workflow.ID); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign-owner workflow error = %v", err)
	}

	_, err = moduleB.Execute(context.Background(), testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: 3,
		IdempotencyKey:   "old-upload-old-revision",
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("old revision execution error = %v", err)
	}
	_, err = moduleB.Execute(context.Background(), testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		IdempotencyKey:   "upload-before-reprepare",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("upload before reprepare error = %v", err)
	}
	reprepared := executeCommand(t, moduleB, PrepareReleaseCommand{
		WorkflowID:       state.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		},
		IdempotencyKey: "fresh-preparation-after-restart",
	})
	if reprepared.Workflow.Release == nil || reprepared.Workflow.DryRun != nil || reprepared.Workflow.UploadResult != nil ||
		len(reprepared.Workflow.RequiredActions) != 0 || len(reprepared.Workflow.Failures) != 0 {
		t.Fatalf("reused preparation after restart = %#v", reprepared.Workflow)
	}
}

func TestPersistentWorkflowOperationRestartIsInterrupted(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "release-workflow-operation.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-operation-restart"})
	fingerprint, err := api.CanonicalWorkflowFingerprint(map[string]string{"command": "prepare_release"})
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("fingerprint operation: %v", err)
	}
	operationID := api.WorkflowOperationID("operation-restart")
	_, _, err = persistentA.CreateOperation(context.Background(), api.ReleaseWorkflowOperationRecord{
		OwnerID:            testOwnerID,
		WorkflowID:         created.Workflow.ID,
		OperationID:        operationID,
		ExpectedRevision:   created.Workflow.Revision,
		IdempotencyKey:     "prepare-restart",
		CommandFingerprint: fingerprint,
		ProcessEpoch:       "epoch-a",
		Status: api.WorkflowOperationStatus{
			ID:         operationID,
			WorkflowID: created.Workflow.ID,
			Revision:   created.Workflow.Revision,
			Sequence:   1,
			Command:    "prepare_release",
			Operation:  api.OperationKindPreparation,
			Status:     api.StageStatusRunning,
			StartedAt:  now,
			UpdatedAt:  now,
		},
	})
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("create running operation: %v", err)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Minute)}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	if _, err := moduleB.Current(context.Background(), testOwnerID, created.Workflow.ID); err != nil {
		t.Fatalf("load workflow after restart: %v", err)
	}
	operation, err := moduleB.Operation(context.Background(), testOwnerID, created.Workflow.ID, operationID)
	if err != nil {
		t.Fatalf("load interrupted operation: %v", err)
	}
	if operation.Status != api.StageStatusInterrupted || operation.Sequence != 2 || operation.CompletedAt == nil {
		t.Fatalf("interrupted durable operation = %#v", operation)
	}
}

func TestPersistentWorkflowSuccessfulOperationReplayRetainsValidResultAfterRestart(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "release-workflow-stale-replay.sqlite")
	repoA, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("open first repository: %v", err)
	}
	if err := repoA.Migrate(); err != nil {
		_ = repoA.Close()
		t.Fatalf("migrate first repository: %v", err)
	}
	persistentA, err := NewPersistentRepository(repoA)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first persistent repository: %v", err)
	}
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-stale-replay"})
	command := PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP"),
		},
		IdempotencyKey: "prepare-stale-replay",
	}
	operation, err := moduleA.Start(context.Background(), testOwnerID, command)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("start preparation: %v", err)
	}
	terminal := waitForWorkflowOperation(t, moduleA, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCompleted
	})
	if terminal.Result == nil || terminal.Result.Kind != api.WorkflowOperationResultRelease {
		_ = repoA.Close()
		t.Fatalf("successful operation result = %#v", terminal.Result)
	}
	if err := repoA.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}

	repoB, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	t.Cleanup(func() { _ = repoB.Close() })
	if err := repoB.Migrate(); err != nil {
		t.Fatalf("migrate reopened repository: %v", err)
	}
	persistentB, err := NewPersistentRepository(repoB)
	if err != nil {
		t.Fatalf("new reopened persistent repository: %v", err)
	}
	moduleB, err := New(
		persistentB,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Minute)}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	replayed, err := moduleB.Start(context.Background(), testOwnerID, command)
	if err != nil {
		t.Fatalf("replay preparation after restart: %v", err)
	}
	if replayed.ID != operation.ID || replayed.Status != api.StageStatusCompleted || replayed.Result == nil ||
		replayed.Result.Kind != api.WorkflowOperationResultRelease || replayed.ResultRevision != 2 ||
		len(replayed.Failures) != 0 {
		t.Fatalf("valid replay = %#v", replayed)
	}
}

func TestNormalizeStateMigratesLegacySurfacePolicyAndFinalApproval(t *testing.T) {
	t.Parallel()

	legacyComposite := State{
		Workflow: api.ReleaseWorkflow{
			Status: api.WorkflowStatusBlocked,
			RequiredActions: []api.RequiredAction{{
				Kind:   api.RequiredActionApproveUpload, //nolint:staticcheck // Construct retained v1 state.
				Status: api.RequiredActionStatusPending,
			}},
		},
		Composite: &compositeUploadSession{},
	}
	normalizeState(&legacyComposite)
	if legacyComposite.TrackerDecisionMode != TrackerDecisionModePostDupeGate {
		t.Fatalf("legacy composite tracker decision mode = %q", legacyComposite.TrackerDecisionMode)
	}
	if len(legacyComposite.Workflow.RequiredActions) != 0 ||
		legacyComposite.Workflow.Status != api.WorkflowStatusActive ||
		legacyComposite.Workflow.TrackerApproval != nil {
		t.Fatalf("legacy composite final approval migration = %#v", legacyComposite.Workflow)
	}

	legacyNonComposite := State{}
	normalizeState(&legacyNonComposite)
	if legacyNonComposite.TrackerDecisionMode != TrackerDecisionModeWebUIControls {
		t.Fatalf("legacy non-composite tracker decision mode = %q", legacyNonComposite.TrackerDecisionMode)
	}
}
