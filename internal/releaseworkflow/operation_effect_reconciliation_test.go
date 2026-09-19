// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAsyncSubmissionUnknownOutcomeReconcilesAndAllowsExactRetry(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo, err := db.Open(filepath.Join(t.TempDir(), "submission-reconciliation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := api.NewSubmissionContentIdentity(api.SubmissionContentScopeSingleFile, []api.SubmissionContentFile{{
		Size:   1,
		SHA256: strings.Repeat("a", 64),
	}})
	if err != nil {
		t.Fatal(err)
	}
	uploads := &submissionUnknownUploadPlanBuilder{
		identity: identity,
		semantic: testFingerprint(t, "submission-unknown-alpha"),
	}
	dupes := dupeAssessmentBuilderFunc(func(
		_ context.Context,
		_ api.DuplicateSubject,
		projections api.TrackerReleaseProjectionSet,
		_ api.TrackerPreflightAssessment,
		now time.Time,
		_ bool,
	) (api.DupeAssessment, any, error) {
		results := make([]api.TrackerDupeAssessment, 0, len(projections.Projections))
		for _, projection := range projections.Projections {
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				return api.DupeAssessment{}, nil, fmt.Errorf("fingerprint submission reconciliation projection: %w", err)
			}
			results = append(results, api.TrackerDupeAssessment{
				TrackerID:             projection.TrackerID,
				UploadReleaseName:     projection.UploadReleaseName,
				ProjectionFingerprint: fingerprint,
				CriteriaFingerprint:   projection.CriteriaFingerprint,
				TargetFingerprint:     projection.DuplicateTargetFingerprint,
				SearchFingerprint:     projection.DuplicateSearchFingerprint,
				PolicyID:              projection.DuplicatePolicyID,
				PolicyFingerprint:     projection.DuplicatePolicyFingerprint,
				Criteria:              projection.DuplicateCriteria,
				Search:                api.DupeSearchEvidence{Complete: true},
				Decision:              api.DupeDecisionNoMatch,
				Status:                api.StageStatusCompleted,
				CheckedAt:             now,
				FreshUntil:            now.Add(time.Hour),
			})
		}
		return api.DupeAssessment{
			InputFingerprint: testFingerprint(t, "submission-unknown-dupes"),
			Results:          results,
			Status:           api.StageStatusCompleted,
			ExpiresAt:        now.Add(time.Hour),
		}, struct{}{}, nil
	})
	media := mediaArtifactBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseRef,
		projections api.TrackerReleaseProjectionSet,
		_ api.MediaCaptureInstructions,
		_ time.Time,
	) (api.MediaArtifactSet, any, error) {
		requirements, err := mediaRequirementsFingerprint(projections.Projections)
		if err != nil {
			return api.MediaArtifactSet{}, nil, err
		}
		return api.MediaArtifactSet{
			CaptureFingerprint:      testFingerprint(t, "submission-unknown-capture"),
			RequirementsFingerprint: requirements,
			Artifacts: []api.MediaArtifact{{
				ID:       "submission-unknown-artifact",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
			}},
			Status: api.StageStatusCompleted,
		}, struct{}{}, nil
	})
	module, err := New(
		persistent,
		NewMemoryPrivateResourceStore(),
		testPreparer(),
		WithClock(fixedClock{now: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithTrackerPreflightBuilder(readyPreflightBuilder(t)),
		WithDupeAssessmentBuilder(dupes),
		WithMediaArtifactBuilder(media),
		WithDescriptionBuilder(&descriptionBuilderFake{testing: t}),
		WithUploadPlanBuilder(uploads),
	)
	if err != nil {
		t.Fatal(err)
	}

	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-async-submission-unknown"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\async-submission-unknown.mkv"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA"}},
	})
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot: api.TrackerReleaseProjectionSet{
			InputFingerprint:  testFingerprint(t, "submission-unknown-projections"),
			PolicyFingerprint: testFingerprint(t, "submission-unknown-policy"),
			Projections:       []api.TrackerReleaseProjection{testProjection(t, "ALPHA", "Example.Release.2026.ALPHA-GRP")},
			Status:            api.StageStatusReady,
		},
	})
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	result = executeCommand(t, module, CheckDuplicatesCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	result = executeCommand(t, module, CaptureMediaCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Instructions:     api.MediaCaptureInstructions{ScreenshotCount: 1, Purpose: api.ScreenshotPurposeFinal},
	})
	result = executeCommand(t, module, GenerateDescriptionsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Instructions:     api.DescriptionInstructions{TemplateVersion: "submission-unknown"},
	})
	result = executeCommand(t, module, DryRunUploadsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		NoSeed:           true,
	})
	if result.DryRun == nil {
		t.Fatalf("prepare reviewed upload plan = %#v", result)
	}

	authority := activateSubmissionReconciliationInput(ctx, t, repo, result.Workflow, time.Now().UTC())
	ctx = api.WithActiveInputAuthority(ctx, authority)
	started, err := module.Start(ctx, testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		NoSeed:           true,
		IdempotencyKey:   "submission-unknown-first",
	})
	if err != nil {
		t.Fatalf("start unknown submission = %v", err)
	}
	operation := waitForWorkflowOperation(t, module, result.Workflow.ID, started.ID, func(status api.WorkflowOperationStatus) bool {
		return isTerminalProgressStatus(status.Status)
	})
	if operation.Status != api.StageStatusExecuted {
		t.Fatalf("unknown submission operation = %#v", operation)
	}

	blocked, err := module.Current(ctx, testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Workflow.Status != api.WorkflowStatusBlocked || len(blocked.Workflow.RequiredActions) != 1 ||
		blocked.Workflow.RequiredActions[0].Kind != api.RequiredActionReconcileSubmission {
		t.Fatalf("unknown submission did not create reconciliation action: %#v", blocked.Workflow)
	}
	action := blocked.Workflow.RequiredActions[0]
	fence, err := repo.LoadSubmissionFence(ctx, identity, submissionUnknownTrackerSite)
	if err != nil {
		t.Fatalf("load submission fence after worker completion: %v", err)
	}
	if fence.Status != api.WorkflowEffectStatusUnknown {
		t.Fatalf("unfinished async submission fence = %#v, want unknown", fence)
	}

	reconciled, err := module.Execute(ctx, testOwnerID, ResolveActionCommand{
		WorkflowID:       blocked.Workflow.ID,
		ExpectedRevision: blocked.Workflow.Revision,
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: blocked.Workflow.Revision,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
		IdempotencyKey: "submission-unknown-not-completed",
	})
	if err != nil {
		t.Fatalf("confirm submission not completed = %v", err)
	}
	if reconciled.Workflow.Status != api.WorkflowStatusActive || len(reconciled.Workflow.RequiredActions) != 0 ||
		reconciled.Workflow.UploadResult != nil {
		t.Fatalf("reconciled workflow = %#v", reconciled.Workflow)
	}
	if _, err := repo.LoadSubmissionFence(ctx, identity, submissionUnknownTrackerSite); !errors.Is(err, api.ErrSubmissionFenceNotFound) {
		t.Fatalf("reconciled submission fence = %v, want not found", err)
	}

	retry, err := module.Start(ctx, testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       reconciled.Workflow.ID,
		ExpectedRevision: reconciled.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		NoSeed:           true,
		IdempotencyKey:   "submission-unknown-retry",
	})
	if err != nil {
		t.Fatalf("start reconciled submission retry = %v", err)
	}
	waitForWorkflowOperation(t, module, reconciled.Workflow.ID, retry.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusExecuted
	})
	if uploads.attempts != 2 {
		t.Fatalf("submission attempts = %d, want 2", uploads.attempts)
	}
}

const submissionUnknownTrackerSite = "ALPHA|https://alpha.example/"

type submissionUnknownUploadPlanBuilder struct {
	identity api.SubmissionContentIdentity
	semantic api.WorkflowFingerprint
	attempts int
}

func (b *submissionUnknownUploadPlanBuilder) Fingerprint(
	_ context.Context,
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	media api.MediaArtifactSet,
	descriptions api.DescriptionSet,
	options UploadPlanBuildOptions,
) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Projections  api.TrackerReleaseProjectionSetRef
		Dupes        api.DupeAssessmentRef
		Media        api.MediaArtifactSetRef
		Descriptions api.DescriptionSetRef
		NoSeed       bool
		TrackerIDs   []api.TrackerID
	}{
		Projections:  api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Dupes:        api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision},
		Media:        api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision},
		Descriptions: api.DescriptionSetRef{ID: descriptions.ID, Revision: descriptions.Revision},
		NoSeed:       options.NoSeed,
		TrackerIDs:   options.TrackerIDs,
	})
	if err != nil {
		return "", fmt.Errorf("submission unknown upload fingerprint: %w", err)
	}
	return fingerprint, nil
}

func (b *submissionUnknownUploadPlanBuilder) Build(
	ctx context.Context,
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	_ any,
	media api.MediaArtifactSet,
	_ any,
	descriptions api.DescriptionSet,
	_ any,
	options UploadPlanBuildOptions,
	now time.Time,
) (api.UploadPlan, RetainedUploadExecution, error) {
	fingerprint, err := b.Fingerprint(ctx, projections, dupes, media, descriptions, options)
	if err != nil {
		return api.UploadPlan{}, nil, err
	}
	return api.UploadPlan{
		InputFingerprint: fingerprint,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Dupes:            api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision},
		Media:            &api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision},
		Descriptions:     &api.DescriptionSetRef{ID: descriptions.ID, Revision: descriptions.Revision},
		Trackers: []api.UploadPlanTracker{{
			TrackerID:             "ALPHA",
			DisplayName:           "Alpha",
			UploadReleaseName:     "Example.Release.2026.ALPHA-GRP",
			Eligible:              true,
			PreparedOperationID:   "submission-unknown-alpha",
			Status:                api.StageStatusReady,
			ClientInjectionStatus: api.StageStatusSkipped,
			SemanticFingerprint:   b.semantic,
		}},
		Status:    api.StageStatusReady,
		ExpiresAt: now.Add(time.Hour),
	}, &submissionUnknownUploadExecution{builder: b}, nil
}

func (*submissionUnknownUploadPlanBuilder) RetryClientInjections(
	context.Context,
	RegisteredArtifactAuthority,
	[]api.TrackerID,
) ([]api.UploadTrackerResult, error) {
	return nil, errors.New("submission unknown upload builder has no client injection retry")
}

type submissionUnknownUploadExecution struct {
	builder *submissionUnknownUploadPlanBuilder
}

func (*submissionUnknownUploadExecution) ResolveAction(
	context.Context,
	api.TrackerID,
	api.RequiredActionKind,
	bool,
) (api.UploadPlanTracker, error) {
	return api.UploadPlanTracker{}, errors.New("submission unknown upload execution has no tracker actions")
}

func (e *submissionUnknownUploadExecution) Execute(ctx context.Context, trackerIDs []api.TrackerID) ([]api.UploadTrackerResult, error) {
	if len(trackerIDs) != 1 || trackerIDs[0] != "ALPHA" {
		return nil, fmt.Errorf("unexpected submission retry targets: %v", trackerIDs)
	}
	e.builder.attempts++
	receipt, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
		Kind:                api.WorkflowExternalEffectTrackerSubmission,
		ScopeID:             "ALPHA",
		SemanticFingerprint: e.builder.semantic,
		Submission: &api.SubmissionFenceAuthority{
			ContentIdentity: e.builder.identity,
			TrackerSite:     submissionUnknownTrackerSite,
			CoordinatorID:   "submission-reconciliation",
			Fence:           1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("begin test submission effect: %w", err)
	}
	if e.builder.attempts == 1 {
		return []api.UploadTrackerResult{{
			TrackerID:        "ALPHA",
			Status:           api.StageStatusFailed,
			SubmissionStatus: api.StageStatusFailed,
			Failures: []api.WorkflowFailure{{
				TrackerID: "ALPHA",
				Failure: api.OperationFailure{
					Code:      api.OperationFailureUnknownOutcome,
					Operation: api.OperationKindUploadExecute,
					Message:   "The submission outcome is unknown.",
					Recovery:  api.OperationRecoveryConfirm,
				},
			}},
		}}, nil
	}
	if err := api.CompleteWorkflowExternalEffect(ctx, receipt, false); err != nil {
		return nil, fmt.Errorf("complete test submission effect: %w", err)
	}
	return []api.UploadTrackerResult{{
		TrackerID:        "ALPHA",
		Status:           api.StageStatusCompleted,
		SubmissionStatus: api.StageStatusCompleted,
	}}, nil
}

func (*submissionUnknownUploadExecution) RegisteredArtifactAuthority() RegisteredArtifactAuthority {
	return RegisteredArtifactAuthority{}
}

func (*submissionUnknownUploadExecution) Release() error { return nil }

func activateSubmissionReconciliationInput(
	ctx context.Context,
	t *testing.T,
	repo *db.SQLiteRepository,
	workflow api.ReleaseWorkflow,
	now time.Time,
) api.ActiveInputAuthority {
	t.Helper()
	empty, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        testOwnerID,
		CoordinatorID:  "submission-reconciliation",
		LeaseExpiresAt: now.Add(time.Hour),
		ReservationID:  "submission-reconciliation-reservation",
		RequestedPath:  "C:\\releases\\async-submission-unknown.mkv",
		IdempotencyKey: "submission-reconciliation-open",
	}
	if err := repo.CompareAndSwapActiveInput(ctx, empty, opening, now); err != nil {
		t.Fatal(err)
	}
	active := opening
	active.State = api.ActiveInputActive
	active.Revision++
	active.InputID = "submission-reconciliation-input"
	active.SourceVersion = "submission-reconciliation-source"
	active.WorkflowID = workflow.ID
	active.ReservationID = ""
	active.RequestedPath = ""
	if err := repo.CompareAndSwapActiveInput(ctx, opening, active, now); err != nil {
		t.Fatal(err)
	}
	return api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence}
}
