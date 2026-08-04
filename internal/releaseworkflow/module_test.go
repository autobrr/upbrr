// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const testOwnerID = "owner-1"

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type mutableClock struct{ now time.Time }

func (c *mutableClock) Now() time.Time { return c.now }

type countingOperationRepository struct {
	*MemoryRepository
	saves atomic.Int32
}

type failOnceSaveRepository struct {
	Repository
	saves int
}

type failOncePutPrivateResourceStore struct {
	PrivateResourceStore
	failed bool
}

func (s *failOncePutPrivateResourceStore) Put(
	ownerID string,
	workflowID api.WorkflowID,
	resourceID string,
	value any,
	expiresAt time.Time,
) error {
	if !s.failed {
		s.failed = true
		return errors.New("synthetic private resource put failure")
	}
	if err := s.PrivateResourceStore.Put(ownerID, workflowID, resourceID, value, expiresAt); err != nil {
		return fmt.Errorf("delegate private resource put: %w", err)
	}
	return nil
}

func (r *failOnceSaveRepository) Save(
	ctx context.Context,
	ownerID string,
	expectedRevision api.WorkflowRevision,
	state State,
) error {
	r.saves++
	if r.saves == 1 {
		return errors.New("synthetic repository save failure")
	}
	if err := r.Repository.Save(ctx, ownerID, expectedRevision, state); err != nil {
		return fmt.Errorf("save wrapped repository: %w", err)
	}
	return nil
}

type panicProgressOperationRepository struct {
	*MemoryRepository
	panicked atomic.Bool
}

type blockingCompleteWorkRepository struct {
	*MemoryRepository
	completeStarted chan struct{}
	releaseComplete chan struct{}
}

type failOnceTerminalOperationSaveRepository struct {
	*MemoryRepository
	failed     atomic.Bool
	saveFailed chan struct{}
}

func (r *blockingCompleteWorkRepository) CompleteWork(ctx context.Context, record api.ReleaseWorkflowWorkRecord) error {
	close(r.completeStarted)
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait to complete operation work: %w", ctx.Err())
	case <-r.releaseComplete:
	}
	return r.MemoryRepository.CompleteWork(ctx, record)
}

func (r *failOnceTerminalOperationSaveRepository) SaveOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	if isTerminalProgressStatus(record.Status.Status) && !r.failed.Swap(true) {
		close(r.saveFailed)
		return errors.New("synthetic terminal operation save failure")
	}
	return r.MemoryRepository.SaveOperation(ctx, expectedSequence, record)
}

func (r *panicProgressOperationRepository) SaveOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	if len(record.Status.Items) > 0 && !r.panicked.Swap(true) {
		panic("progress persistence panic")
	}
	return r.MemoryRepository.SaveOperation(ctx, expectedSequence, record)
}

func (r *countingOperationRepository) SaveOperation(
	ctx context.Context,
	expectedSequence uint64,
	record api.ReleaseWorkflowOperationRecord,
) error {
	r.saves.Add(1)
	return r.MemoryRepository.SaveOperation(ctx, expectedSequence, record)
}

type sequenceIDGenerator struct{ next int }

func (g *sequenceIDGenerator) NewID(prefix string) (string, error) {
	g.next++
	return fmt.Sprintf("%s-%d", prefix, g.next), nil
}

func TestStampProjectionActionsPublishesDurableActionIdentity(t *testing.T) {
	t.Parallel()

	module := &Module{ids: &sequenceIDGenerator{}}
	now := time.Date(2026, time.July, 28, 20, 0, 0, 0, time.UTC)
	snapshot := api.TrackerReleaseProjectionSet{
		RequiredActions: []api.RequiredAction{{Kind: api.RequiredActionProvideTrackerInput}},
		Projections: []api.TrackerReleaseProjection{{
			TrackerID: "AR",
			RequiredActions: []api.RequiredAction{{
				Kind:           api.RequiredActionProvideTrackerInput,
				Prompt:         "Confirm release name.",
				AllowsFreeText: true,
			}},
		}},
	}
	if err := module.stampProjectionActions(&snapshot, 7, now); err != nil {
		t.Fatalf("stamp projection actions: %v", err)
	}
	if len(snapshot.RequiredActions) != 1 || len(snapshot.Projections[0].RequiredActions) != 1 {
		t.Fatalf("stamped actions = %#v", snapshot)
	}
	projectionAction := snapshot.Projections[0].RequiredActions[0]
	snapshotAction := snapshot.RequiredActions[0]
	if projectionAction.ID == "" || snapshotAction.ID != projectionAction.ID ||
		projectionAction.TrackerID != "AR" || projectionAction.WorkflowRevision != 7 ||
		projectionAction.Status != api.RequiredActionStatusPending || !projectionAction.CreatedAt.Equal(now) {
		t.Fatalf("projection action = %#v, snapshot action = %#v", projectionAction, snapshotAction)
	}
}

type trackerProjectionBuilderFunc func(
	context.Context,
	api.ReleaseSnapshot,
	api.UploadSubject,
	[]api.TrackerID,
	map[api.TrackerID]api.TrackerProjectionInstructions,
	api.WorkflowExecutionMode,
) (
	api.TrackerCatalogSnapshot,
	api.TrackerRuntimeSnapshot,
	api.TrackerSelection,
	api.TrackerReleaseProjectionSet,
	error,
)

func (f trackerProjectionBuilderFunc) Build(
	ctx context.Context,
	release api.ReleaseSnapshot,
	subject api.UploadSubject,
	trackerIDs []api.TrackerID,
	instructions map[api.TrackerID]api.TrackerProjectionInstructions,
	executionMode api.WorkflowExecutionMode,
) (
	api.TrackerCatalogSnapshot,
	api.TrackerRuntimeSnapshot,
	api.TrackerSelection,
	api.TrackerReleaseProjectionSet,
	error,
) {
	return f(ctx, release, subject, trackerIDs, instructions, executionMode)
}

type trackerPreflightBuilderFunc func(
	context.Context,
	api.UploadSubject,
	api.TrackerCatalogSnapshot,
	api.TrackerRuntimeSnapshot,
	api.TrackerReleaseProjectionSet,
	time.Time,
) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error)

func (f trackerPreflightBuilderFunc) Build(
	ctx context.Context,
	subject api.UploadSubject,
	catalog api.TrackerCatalogSnapshot,
	runtime api.TrackerRuntimeSnapshot,
	projections api.TrackerReleaseProjectionSet,
	now time.Time,
) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
	return f(ctx, subject, catalog, runtime, projections, now)
}

type dupeAssessmentBuilderFunc func(
	context.Context,
	api.DuplicateSubject,
	api.TrackerReleaseProjectionSet,
	api.TrackerPreflightAssessment,
	time.Time,
	bool,
) (api.DupeAssessment, any, error)

func (f dupeAssessmentBuilderFunc) Build(
	ctx context.Context,
	subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet,
	preflight api.TrackerPreflightAssessment,
	now time.Time,
	skipRemote bool,
) (api.DupeAssessment, any, error) {
	return f(ctx, subject, projections, preflight, now, skipRemote)
}

type mediaArtifactBuilderFunc func(
	context.Context,
	api.ReleaseRef,
	api.TrackerReleaseProjectionSet,
	api.MediaCaptureInstructions,
	time.Time,
) (api.MediaArtifactSet, any, error)

func (f mediaArtifactBuilderFunc) Build(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	instructions api.MediaCaptureInstructions,
	now time.Time,
) (api.MediaArtifactSet, any, error) {
	return f(ctx, release, projections, instructions, now)
}

type descriptionBuilderFake struct {
	testing *testing.T
	builds  int
}

type retainedMediaResourceFake struct {
	stats   *retainedMediaResourceStats
	pending bool
}

type retainedMediaResourceStats struct {
	staged    int
	deletions int
	commitErr error
}

func (*retainedMediaResourceFake) OpenArtifact(
	context.Context,
	api.MediaArtifactSet,
	api.PublicResourceID,
) (MediaArtifactContent, error) {
	return MediaArtifactContent{
		Body:        io.NopCloser(strings.NewReader("synthetic image")),
		ContentType: "image/png",
	}, nil
}

func (f *retainedMediaResourceFake) DeleteArtifacts(
	_ context.Context,
	_ api.MediaArtifactSet,
	_ []api.PublicResourceID,
) (RetainedMediaResource, error) {
	f.stats.staged++
	return &retainedMediaResourceFake{stats: f.stats, pending: true}, nil
}

func (f *retainedMediaResourceFake) Commit(context.Context) error {
	if f.stats.commitErr != nil {
		return f.stats.commitErr
	}
	if f.pending {
		f.stats.deletions++
		f.pending = false
	}
	return nil
}

type retainedUploadExecutionFake struct {
	executions int
	releases   int
	trackers   []api.TrackerID
	selected   []api.TrackerID
	failed     map[api.TrackerID]bool
}

func (f *retainedUploadExecutionFake) Execute(_ context.Context, trackerIDs []api.TrackerID) ([]api.UploadTrackerResult, error) {
	f.executions++
	f.selected = append([]api.TrackerID(nil), trackerIDs...)
	results := make([]api.UploadTrackerResult, 0, len(f.trackers))
	for _, trackerID := range f.trackers {
		if len(trackerIDs) > 0 && !slices.Contains(trackerIDs, trackerID) {
			continue
		}
		if f.failed[trackerID] {
			results = append(results, api.UploadTrackerResult{
				TrackerID: trackerID,
				Status:    api.StageStatusFailed,
				Failures: []api.WorkflowFailure{{
					Failure: api.OperationFailure{
						Code:      api.OperationFailureInternal,
						Operation: api.OperationKindUploadExecute,
						Message:   "Synthetic tracker upload failure.",
						Recovery:  api.OperationRecoveryRetry,
					},
					TrackerID: trackerID,
				}},
			})
			continue
		}
		results = append(results, api.UploadTrackerResult{
			TrackerID: trackerID,
			Status:    api.StageStatusCompleted,
			RemoteID:  "123",
			RemoteURL: "https://tracker.example/torrents/123",
		})
	}
	return results, nil
}

func (f *retainedUploadExecutionFake) RegisteredArtifactAuthority() RegisteredArtifactAuthority {
	return RegisteredArtifactAuthority{}
}

func TestCompleteUploadExecutionResultsReportsSkippedAndBlockedTrackers(t *testing.T) {
	t.Parallel()

	completed := completeUploadExecutionResults(
		[]api.UploadPlanTracker{
			{
				TrackerID: "ALPHA",
				Eligible:  true,
				Status:    api.StageStatusReady,
			},
			{TrackerID: "BETA", Status: api.StageStatusSkipped},
			{TrackerID: "GAMMA", Status: api.StageStatusBlocked},
		},
		[]api.UploadTrackerResult{{TrackerID: "ALPHA", Status: api.StageStatusCompleted}},
		nil,
		nil,
	)
	if len(completed) != 3 || completed[0].TrackerID != "ALPHA" || completed[0].Status != api.StageStatusCompleted ||
		completed[1].TrackerID != "BETA" || completed[1].Status != api.StageStatusSkipped ||
		completed[2].TrackerID != "GAMMA" || completed[2].Status != api.StageStatusFailed || len(completed[2].Failures) != 1 {
		t.Fatalf("completed upload results = %#v", completed)
	}
}

func TestValidateUploadPlanBuildRequiresExactDownstreamTrackerSet(t *testing.T) {
	t.Parallel()

	inputFingerprint := testFingerprint(t, "upload-plan-subset")
	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:         "ALPHA",
			UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
			DescriptionGroup:  "unit3d",
		},
		{
			TrackerID:         "BETA",
			UploadReleaseName: "Example.Release.2026.BETA-GRP",
			DescriptionGroup:  "unit3d",
		},
	}}
	plan := api.UploadPlan{
		InputFingerprint: inputFingerprint,
		Trackers: []api.UploadPlanTracker{{
			TrackerID:         "ALPHA",
			UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
			DescriptionGroup:  "unit3d",
		}},
	}

	if err := validateUploadPlanBuild(projections, inputFingerprint, plan); err == nil {
		t.Fatal("validate upload plan accepted an omitted downstream tracker")
	}
	plan.Trackers = append(plan.Trackers, api.UploadPlanTracker{
		TrackerID:         "GAMMA",
		UploadReleaseName: "Example.Release.2026.GAMMA-GRP",
		DescriptionGroup:  "unit3d",
	})
	if err := validateUploadPlanBuild(projections, inputFingerprint, plan); err == nil {
		t.Fatal("validate upload plan accepted an unselected tracker")
	}
}

func TestDescriptionViabilityUsesTrackerOutcomesInsteadOfAggregateStatus(t *testing.T) {
	t.Parallel()

	partial := api.DescriptionSet{
		Status: api.StageStatusFailed,
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "ALPHA", Status: api.StageStatusCompleted},
			{TrackerID: "BETA", Status: api.StageStatusFailed},
		},
	}
	if !descriptionsHaveViableTracker(&partial) {
		t.Fatal("successful tracker lane did not allow upload planning")
	}
	failed := api.DescriptionSet{
		Status: api.StageStatusFailed,
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "ALPHA", Status: api.StageStatusFailed},
		},
	}
	if descriptionsHaveViableTracker(&failed) {
		t.Fatal("all-failed tracker lanes allowed upload planning")
	}
}

func TestUploadDryRunReportsPreserveTrackerSpecificFailureMessages(t *testing.T) {
	t.Parallel()

	reports := uploadDryRunReports([]api.UploadPlanTracker{
		{
			TrackerID: "ALPHA",
			Status:    api.StageStatusBlocked,
			Warnings:  []string{"Required tracker category could not be resolved."},
		},
		{
			TrackerID:              "BETA",
			Status:                 api.StageStatusReady,
			ClientInjectionStatus:  api.StageStatusFailed,
			ClientInjectionMessage: "Client injection failed because no prepared tracker torrent is available.",
		},
	})

	if len(reports) != 2 || len(reports[0].Failures) != 1 ||
		reports[0].Failures[0].Failure.Message != "Required tracker category could not be resolved." {
		t.Fatalf("tracker preparation dry-run report = %#v", reports)
	}
	if len(reports[1].Failures) != 1 ||
		reports[1].Failures[0].Failure.Message != "Client injection failed because no prepared tracker torrent is available." {
		t.Fatalf("client injection dry-run report = %#v", reports)
	}
}

func (f *retainedUploadExecutionFake) Release() error {
	f.releases++
	return nil
}

type uploadPlanBuilderFake struct {
	testing       *testing.T
	builds        int
	options       []UploadPlanBuildOptions
	execution     *retainedUploadExecutionFake
	failed        map[api.TrackerID]bool
	clientRetries int
}

func (f *uploadPlanBuilderFake) Fingerprint(
	_ context.Context,
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	media api.MediaArtifactSet,
	descriptions api.DescriptionSet,
	options UploadPlanBuildOptions,
) (api.WorkflowFingerprint, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Projections  api.TrackerReleaseProjectionSetID
		Dupes        api.DupeAssessmentID
		Media        api.MediaArtifactSetID
		Descriptions api.DescriptionSetID
		NoSeed       bool
		TrackerIDs   []api.TrackerID
	}{projections.ID, dupes.ID, media.ID, descriptions.ID, options.NoSeed, options.TrackerIDs})
	if err != nil {
		return "", fmt.Errorf("upload plan input fingerprint: %w", err)
	}
	return fingerprint, nil
}

func (f *uploadPlanBuilderFake) Build(
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
	f.builds++
	f.options = append(f.options, options)
	fingerprint, err := f.Fingerprint(ctx, projections, dupes, media, descriptions, options)
	if err != nil {
		return api.UploadPlan{}, nil, err
	}
	trackers := make([]api.UploadPlanTracker, 0, len(projections.Projections))
	f.execution = &retainedUploadExecutionFake{failed: f.failed}
	for _, projection := range projections.Projections {
		if len(options.TrackerIDs) > 0 && !slices.Contains(options.TrackerIDs, projection.TrackerID) {
			continue
		}
		semantic := testFingerprint(f.testing, "upload-plan-"+string(projection.TrackerID))
		clientStatus := api.StageStatus("")
		clientMessage := ""
		if options.DryRun {
			clientStatus = api.StageStatusCompleted
			clientMessage = "Client injection completed."
			if options.NoSeed {
				clientStatus = api.StageStatusSkipped
				clientMessage = "Client injection disabled by the skip option."
			}
		}
		trackers = append(trackers, api.UploadPlanTracker{
			TrackerID:              projection.TrackerID,
			DisplayName:            projection.DisplayName,
			UploadReleaseName:      projection.UploadReleaseName,
			Taxonomy:               projection.Taxonomy,
			DescriptionGroup:       projection.DescriptionGroup,
			Eligible:               true,
			PreparedOperationID:    api.PublicResourceID("prepared-" + strings.ToLower(string(projection.TrackerID))),
			Status:                 api.StageStatusReady,
			ClientInjectionStatus:  clientStatus,
			ClientInjectionMessage: clientMessage,
			SemanticFingerprint:    semantic,
		})
		f.execution.trackers = append(f.execution.trackers, projection.TrackerID)
	}
	return api.UploadPlan{
		InputFingerprint: fingerprint,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Dupes:            api.DupeAssessmentRef{ID: dupes.ID, Revision: dupes.Revision},
		Media:            &api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision},
		Descriptions:     &api.DescriptionSetRef{ID: descriptions.ID, Revision: descriptions.Revision},
		Trackers:         trackers,
		Status:           api.StageStatusReady,
		ExpiresAt:        now.Add(time.Hour),
	}, f.execution, nil
}

func (f *uploadPlanBuilderFake) RetryClientInjections(
	_ context.Context,
	_ RegisteredArtifactAuthority,
	trackerIDs []api.TrackerID,
) ([]api.UploadTrackerResult, error) {
	f.clientRetries++
	results := make([]api.UploadTrackerResult, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		results = append(results, api.UploadTrackerResult{
			TrackerID:              trackerID,
			Status:                 api.StageStatusCompleted,
			SubmissionStatus:       api.StageStatusCompleted,
			ClientInjectionStatus:  api.StageStatusCompleted,
			ClientInjectionMessage: "Client injection completed.",
			ClientInjected:         true,
		})
	}
	return results, nil
}

func (f *descriptionBuilderFake) Fingerprints(
	_ context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	_ any,
	instructions api.DescriptionInstructions,
) (api.WorkflowFingerprint, api.WorkflowFingerprint, error) {
	input, err := api.CanonicalWorkflowFingerprint(struct {
		Release      api.ReleaseRef
		Projections  api.TrackerReleaseProjectionSetRef
		Media        api.MediaArtifactSetID
		Instructions api.DescriptionInstructions
	}{
		Release:      release,
		Projections:  api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision},
		Media:        media.ID,
		Instructions: instructions,
	})
	if err != nil {
		return "", "", fmt.Errorf("description input fingerprint: %w", err)
	}
	return input, testFingerprint(f.testing, "description-template"), nil
}

func (f *descriptionBuilderFake) Build(
	ctx context.Context,
	release api.ReleaseRef,
	projections api.TrackerReleaseProjectionSet,
	media api.MediaArtifactSet,
	_ any,
	instructions api.DescriptionInstructions,
	_ time.Time,
) (api.DescriptionSet, error) {
	f.builds++
	input, template, err := f.Fingerprints(ctx, release, projections, media, nil, instructions)
	if err != nil {
		return api.DescriptionSet{}, err
	}
	overrides := make(map[string]string, len(instructions.Overrides))
	for _, override := range instructions.Overrides {
		overrides[strings.ToLower(strings.TrimSpace(override.GroupKey))] = override.Source
	}
	groups := make([]string, 0, len(projections.Projections))
	trackerIDsByGroup := make(map[string][]api.TrackerID, len(projections.Projections))
	for _, projection := range projections.Projections {
		group := strings.TrimSpace(projection.DescriptionGroup)
		if group == "" {
			group = "alpha"
		}
		if _, exists := trackerIDsByGroup[group]; !exists {
			groups = append(groups, group)
		}
		trackerIDsByGroup[group] = append(trackerIDsByGroup[group], projection.TrackerID)
	}
	rendered := make([]api.RenderedDescription, 0, len(groups))
	for _, group := range groups {
		source := "Example description for " + group + "."
		if override, exists := overrides[strings.ToLower(group)]; exists {
			source = override
		}
		rendered = append(rendered, api.RenderedDescription{
			GroupKey:           group,
			TrackerIDs:         trackerIDsByGroup[group],
			Source:             source,
			Rendered:           "<p>" + source + "</p>",
			ContentFingerprint: testFingerprint(f.testing, "description-content-"+group+"-"+source),
		})
	}
	return api.DescriptionSet{
		InputFingerprint:    input,
		TemplateFingerprint: template,
		Descriptions:        rendered,
		Status:              api.StageStatusCompleted,
	}, nil
}

func TestModuleSequencingIdempotencyRevisionAndOwnerIsolation(t *testing.T) {
	t.Parallel()

	module, _ := newTestModule(t, testPreparer())
	created := executeCommand(t, module, CreateWorkflowCommand{
		WorkflowID:     "workflow-1",
		Instructions:   api.ReleaseFactInstructions{SourceLookup: "Example Release 2026"},
		IdempotencyKey: "create-1",
	})
	if created.Workflow.Revision != 1 || created.FactInstructions == nil {
		t.Fatalf("create result = %#v", created)
	}

	retried := executeCommand(t, module, CreateWorkflowCommand{
		WorkflowID:     "workflow-1",
		Instructions:   api.ReleaseFactInstructions{SourceLookup: "Example Release 2026"},
		IdempotencyKey: "create-1",
	})
	if retried.Workflow.ID != created.Workflow.ID || retried.FactInstructions.ID != created.FactInstructions.ID {
		t.Fatalf("create retry returned a different result: first=%#v retry=%#v", created, retried)
	}
	_, err := module.Execute(context.Background(), testOwnerID, CreateWorkflowCommand{
		WorkflowID:     "workflow-different-authority",
		Instructions:   api.ReleaseFactInstructions{SourceLookup: "Example Release 2026"},
		IdempotencyKey: "create-1",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("create authority reuse error = %v, want %v", err, ErrIdempotencyConflict)
	}

	_, err = module.Execute(context.Background(), testOwnerID, CreateWorkflowCommand{
		Instructions:   api.ReleaseFactInstructions{SourceLookup: "Different Example 2026"},
		IdempotencyKey: "create-1",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("create key reuse error = %v, want %v", err, ErrIdempotencyConflict)
	}

	preparedCommand := PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
			Intent:     api.PreparationIntentPreview,
		},
		IdempotencyKey: "prepare-1",
	}
	prepared := executeCommand(t, module, preparedCommand)
	if prepared.Workflow.Revision != 2 || prepared.Release == nil {
		t.Fatalf("prepare result = %#v", prepared)
	}
	retriedPrepare := executeCommand(t, module, preparedCommand)
	if retriedPrepare.Release.ID != prepared.Release.ID || retriedPrepare.Workflow.Revision != prepared.Workflow.Revision {
		t.Fatalf("prepare retry returned a different revision: first=%#v retry=%#v", prepared, retriedPrepare)
	}

	conflictingPrepare := preparedCommand
	conflictingPrepare.Input.Intent = api.PreparationIntentUpload
	_, err = module.Execute(context.Background(), testOwnerID, conflictingPrepare)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("prepare key reuse error = %v, want %v", err, ErrIdempotencyConflict)
	}

	_, err = module.Execute(context.Background(), testOwnerID, ReplaceFactInstructionsCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: 1,
		Instructions:     api.ReleaseFactInstructions{SourceLookup: "Stale Example 2026"},
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v, want %v", err, ErrRevisionConflict)
	}

	_, err = module.Workflow(context.Background(), "different-owner", created.Workflow.ID)
	if !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign owner query error = %v, want %v", err, ErrWorkflowNotFound)
	}

	prepared.Workflow.Status = api.WorkflowStatusFailed
	queried, err := module.Workflow(context.Background(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("query workflow: %v", err)
	}
	if queried.Status != api.WorkflowStatusActive {
		t.Fatalf("repository state mutated through result: status=%s", queried.Status)
	}
}

func TestModuleResetAndBlurayCandidateSelectionUseExactRetainedAuthority(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	var inputs []api.PrepareInput
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			inputs = append(inputs, input)
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: api.PreparedGeneration(len(inputs)),
				Source:     api.SourceManifest{SourcePath: input.SourcePath},
				Naming:     api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
				ProviderMetadata: api.SourceScopedMetadata{Bluray: &api.BlurayMetadata{
					SelectedReleaseID: input.Instructions.BlurayReleaseID,
					Candidates: []api.BlurayReleaseCandidate{
						{ReleaseID: "candidate-retained", MovieTitle: "Example Release"},
					},
				}},
			}}, nil
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	module, _ := newTestModule(t, preparer)
	result := executeCommand(t, module, CreateWorkflowCommand{
		WorkflowID:     "workflow-reset-candidate",
		Instructions:   api.ReleaseFactInstructions{SourceLookup: "Example Release"},
		IdempotencyKey: "create-reset-candidate",
	})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
		IdempotencyKey:   "prepare-reset-candidate",
	})
	result = executeCommand(t, module, ResetReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath:   "C:\\releases\\Example.Release.2026.1080p-GRP",
			Instructions: api.ReleaseFactInstructions{SourceLookup: "Reset Example Release"},
		},
		IdempotencyKey: "reset-release",
	})
	if !inputs[len(inputs)-1].Force || result.FactInstructions == nil || result.Release == nil {
		t.Fatalf("reset result = %#v, input = %#v", result, inputs[len(inputs)-1])
	}
	if result.FactInstructions.Instructions.SourceLookup != "Reset Example Release" {
		t.Fatalf("reset instructions = %#v", result.FactInstructions.Instructions)
	}

	result = executeCommand(t, module, SelectBlurayCandidateCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		ReleaseID:        "candidate-retained",
		IdempotencyKey:   "select-candidate",
	})
	if result.FactInstructions == nil || result.FactInstructions.Instructions.BlurayReleaseID != "candidate-retained" {
		t.Fatalf("candidate fact instructions = %#v", result.FactInstructions)
	}
	if !inputs[len(inputs)-1].Force || inputs[len(inputs)-1].Instructions.BlurayReleaseID != "candidate-retained" {
		t.Fatalf("candidate prepare input = %#v", inputs[len(inputs)-1])
	}

	_, err := module.Execute(context.Background(), testOwnerID, SelectBlurayCandidateCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		ReleaseID:        "candidate-not-retained",
		IdempotencyKey:   "select-foreign-candidate",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("foreign candidate error = %v, want %v", err, ErrInvalidTransition)
	}
}

func TestModuleBDMVPreparationRequiresTypedPlaylistSelection(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: 1,
				Source: api.SourceManifest{
					SourcePath: input.SourcePath,
					Entries: []api.SourceManifestEntry{{
						Type:     api.SourceEntryTypePlaylist,
						Playlist: "00001.mpls",
					}},
					Classification: api.SourceClassification{DiscType: "BDMV"},
				},
				Naming: api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
			}}, nil
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	module, _ := newTestModule(t, preparer)
	result := executeCommand(t, module, CreateWorkflowCommand{
		WorkflowID:     "workflow-playlist",
		IdempotencyKey: "create-playlist",
	})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example Disc",
			Intent:     api.PreparationIntentPreview,
		},
		IdempotencyKey: "prepare-playlist-required",
	})
	if result.Workflow.Status != api.WorkflowStatusBlocked || len(result.Workflow.RequiredActions) != 1 {
		t.Fatalf("playlist preparation workflow = %#v", result.Workflow)
	}
	action := result.Workflow.RequiredActions[0]
	if action.Kind != api.RequiredActionSelectPlaylist || len(action.Options) != 1 || action.Options[0].Value != "00001.mpls" {
		t.Fatalf("playlist action = %#v", action)
	}

	instructions := api.ReleaseFactInstructions{
		Playlist: api.PlaylistInstruction{Set: true, Selected: []string{"00001.mpls"}},
	}
	result = executeCommand(t, module, ReplaceFactInstructionsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   "select-playlist",
	})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath:   "C:\\releases\\Example Disc",
			Intent:       api.PreparationIntentPreview,
			Instructions: instructions,
		},
		IdempotencyKey: "prepare-playlist-selected",
	})
	if result.Workflow.Status != api.WorkflowStatusActive || len(result.Workflow.RequiredActions) != 0 {
		t.Fatalf("selected playlist workflow = %#v", result.Workflow)
	}
}

func TestModuleSelectiveTrackerInvalidation(t *testing.T) {
	t.Parallel()

	module, repository := newTestModule(t, testPreparer())
	result := executeCommand(t, module, CreateWorkflowCommand{
		WorkflowID:     "workflow-selective",
		IdempotencyKey: "create-selective",
	})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
			Intent:     api.PreparationIntentDuplicateCheck,
		},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testProjectionSet(t),
	})
	module.trackerPreflight = readyPreflightBuilder(t)
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	result = executeTestPublication(t, module, dupeAssessmentPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testDupes(t),
	})

	result = executeCommand(t, module, InvalidateTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"alpha", "ALPHA"},
		Reason:           "tracker config changed",
	})
	if result.Projections == nil || len(result.Projections.Projections) != 1 || result.Projections.Projections[0].TrackerID != "BETA" {
		t.Fatalf("filtered projections = %#v", result.Projections)
	}
	if result.Workflow.Status != api.WorkflowStatusBlocked || len(result.Workflow.RequiredActions) != 1 {
		t.Fatalf("invalidated workflow status/actions = %s/%#v", result.Workflow.Status, result.Workflow.RequiredActions)
	}

	state, err := repository.Load(context.Background(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatalf("load workflow state: %v", err)
	}
	preflight := state.Preflights[state.Workflow.TrackerPreflight.ID]
	if len(preflight.Results) != 1 || preflight.Results[0].TrackerID != "BETA" {
		t.Fatalf("filtered preflight = %#v", preflight.Results)
	}
	dupes := state.Dupes[state.Workflow.Dupes.ID]
	if len(dupes.Results) != 1 || dupes.Results[0].TrackerID != "BETA" {
		t.Fatalf("filtered dupes = %#v", dupes.Results)
	}
	if state.Workflow.Media != nil || state.Workflow.Descriptions != nil || state.Workflow.DryRun != nil || state.Workflow.UploadResult != nil {
		t.Fatalf("dependent refs survived invalidation: %#v", state.Workflow)
	}
}

func TestModuleProjectTrackersOwnsCatalogRuntimeSelectionAndProjectionLineage(t *testing.T) {
	t.Parallel()

	var receivedSubject api.UploadSubject
	builder := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		subject api.UploadSubject,
		trackerIDs []api.TrackerID,
		_ map[api.TrackerID]api.TrackerProjectionInstructions,
		_ api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		receivedSubject = subject
		return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: trackerIDs}, testProjectionSet(t), nil
	})
	module, _ := newTestModule(t, testPreparer(), WithTrackerProjectionBuilder(builder))
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-project"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		},
	})
	name := "Example.Release.2026.ALPHA-GRP"
	result = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
		Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{
			"ALPHA": {UploadReleaseName: api.WorkflowPatch[string]{Present: true, Value: name}},
		},
	})
	if receivedSubject.SourcePath != "C:\\releases\\Example.Release.2026.1080p-GRP" {
		t.Fatalf("projection subject = %#v", receivedSubject)
	}
	if result.Catalog == nil || result.Runtime == nil || result.Selection == nil || result.ProjectionInstructions == nil || result.Projections == nil {
		t.Fatalf("project tracker result = %#v", result)
	}
	if result.Workflow.Revision != 3 || result.Workflow.TrackerCatalog == nil || result.Workflow.TrackerRuntime == nil ||
		result.Workflow.Selection == nil || result.Workflow.ProjectionInstructions == nil || result.Workflow.TrackerProjections == nil {
		t.Fatalf("project tracker lineage = %#v", result.Workflow)
	}
	if result.Projections.Release != *result.Workflow.Release || result.Projections.Catalog != *result.Workflow.TrackerCatalog ||
		result.Projections.Runtime != *result.Workflow.TrackerRuntime || result.Projections.Selection != *result.Workflow.Selection ||
		result.Projections.Instructions == nil || *result.Projections.Instructions != *result.Workflow.ProjectionInstructions {
		t.Fatalf("projection refs do not match aggregate: projections=%#v workflow=%#v", result.Projections, result.Workflow)
	}
}

func TestModuleReviewsTrackerReleaseNameWithoutRepeatingDupeSearch(t *testing.T) {
	t.Parallel()

	const automaticName = "Example.Release.2026.ALPHA-GRP"
	const reviewedName = "Example.Release.2026.REVIEWED-GRP"
	catalog, err := testCatalog(t).WithFingerprint()
	if err != nil {
		t.Fatalf("fingerprint review catalog: %v", err)
	}
	projector := trackerProjectionBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseSnapshot,
		_ api.UploadSubject,
		trackerIDs []api.TrackerID,
		instructions map[api.TrackerID]api.TrackerProjectionInstructions,
		_ api.WorkflowExecutionMode,
	) (
		api.TrackerCatalogSnapshot,
		api.TrackerRuntimeSnapshot,
		api.TrackerSelection,
		api.TrackerReleaseProjectionSet,
		error,
	) {
		projection := testProjection(t, "ALPHA", automaticName)
		projection.PolicyDecisions = []api.TrackerPolicyDecision{{
			Code:     releaseNameConfirmationDecisionCode,
			Decision: "confirmation_required",
			Blocking: false,
		}}
		projection.RequiredActions = []api.RequiredAction{{
			Kind:           api.RequiredActionProvideTrackerInput,
			TrackerID:      "ALPHA",
			Prompt:         "Confirm the tracker release name.",
			AllowsFreeText: true,
		}}
		projection.UploadReady = false
		inputFingerprint := testFingerprint(t, "name-review-automatic")
		policyFingerprint := testFingerprint(t, "name-review-policy-automatic")
		if instruction := instructions["ALPHA"]; instruction.UploadReleaseName.Present {
			projection.UploadReleaseName = instruction.UploadReleaseName.Value
			projection.AdditionalNames = []api.TrackerReleaseName{{
				Role:  api.TrackerReleaseNameRoleSearch,
				Value: automaticName,
			}}
			projection.ProjectorFingerprint = testFingerprint(t, "ALPHA-projector-reviewed")
			projection.PolicyDecisions = []api.TrackerPolicyDecision{{
				Code:     releaseNameConfirmationDecisionCode,
				Decision: "confirmed",
				Blocking: false,
			}}
			projection.RequiredActions = nil
			projection.UploadReady = true
			inputFingerprint = testFingerprint(t, "name-review-reviewed")
			policyFingerprint = testFingerprint(t, "name-review-policy-reviewed")
		}
		projection.InputFingerprint = inputFingerprint
		return catalog, testRuntime(t), api.TrackerSelection{TrackerIDs: trackerIDs}, api.TrackerReleaseProjectionSet{
			InputFingerprint:  inputFingerprint,
			PolicyFingerprint: policyFingerprint,
			Projections:       []api.TrackerReleaseProjection{projection},
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
			return api.TrackerPreflightAssessment{}, nil, fmt.Errorf(
				"fingerprint name-review preflight projection: %w",
				fingerprintErr,
			)
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
			RequiredActions:       append([]api.RequiredAction(nil), projection.RequiredActions...),
			AssessedAt:            now,
			FreshUntil:            now.Add(time.Hour),
		}
		return api.TrackerPreflightAssessment{
			InputFingerprint: testFingerprint(t, "name-review-preflight"),
			Results:          []api.TrackerPreflightResult{result},
			ExpiresAt:        now.Add(time.Hour),
		}, append([]api.TrackerReleaseProjection(nil), initial.Projections...), nil
	})
	dupeChecks := 0
	dupeBuilder := dupeAssessmentBuilderFunc(func(
		_ context.Context,
		_ api.DuplicateSubject,
		projections api.TrackerReleaseProjectionSet,
		_ api.TrackerPreflightAssessment,
		now time.Time,
		_ bool,
	) (api.DupeAssessment, any, error) {
		dupeChecks++
		projection := projections.Projections[0]
		fingerprint, fingerprintErr := api.CanonicalWorkflowFingerprint(projection)
		if fingerprintErr != nil {
			return api.DupeAssessment{}, nil, fmt.Errorf(
				"fingerprint name-review duplicate projection: %w",
				fingerprintErr,
			)
		}
		return api.DupeAssessment{
			InputFingerprint: testFingerprint(t, "name-review-dupes"),
			Results: []api.TrackerDupeAssessment{{
				TrackerID:             projection.TrackerID,
				UploadReleaseName:     projection.UploadReleaseName,
				ProjectionFingerprint: fingerprint,
				CriteriaFingerprint:   projection.CriteriaFingerprint,
				Criteria:              projection.DuplicateCriteria,
				Decision:              api.DupeDecisionNoMatch,
				Status:                api.StageStatusCompleted,
				CheckedAt:             now,
				FreshUntil:            now.Add(time.Hour),
			}},
			Status:    api.StageStatusCompleted,
			ExpiresAt: now.Add(time.Hour),
		}, struct{ Evidence string }{Evidence: "retained"}, nil
	})
	module, _ := newTestModule(
		t,
		testPreparer(),
		WithTrackerProjectionBuilder(projector),
		WithTrackerPreflightBuilder(preflight),
		WithDupeAssessmentBuilder(dupeBuilder),
	)
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-name-review"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA"},
		Instructions:     map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {}},
	})
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	result = executeCommand(t, module, CheckDuplicatesCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	if dupeChecks != 1 || result.Dupes == nil || len(result.Workflow.RequiredActions) != 1 {
		t.Fatalf("initial duplicate result = %#v checks=%d", result, dupeChecks)
	}
	priorDupeRef := *result.Workflow.Dupes
	action := result.Workflow.RequiredActions[0]
	confirmed := true
	reviewedNameValue := reviewedName
	module.private = &failOncePutPrivateResourceStore{PrivateResourceStore: module.private}
	_, err = module.Execute(context.Background(), testOwnerID, ResolveActionCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: result.Workflow.Revision,
			TextValue:        &reviewedNameValue,
			Confirmed:        &confirmed,
		},
		IdempotencyKey: "review-tracker-name-private-put-failure",
	})
	if err == nil || !strings.Contains(err.Error(), "retain duplicate evidence after name review") {
		t.Fatalf("expected duplicate evidence replacement failure, got %v", err)
	}
	if _, err := module.private.Get(
		testOwnerID,
		result.Workflow.ID,
		dupePrivateResourceID(priorDupeRef.ID),
		module.clock.Now().UTC(),
	); err != nil {
		t.Fatalf("prior duplicate evidence lost after replacement failure: %v", err)
	}
	result = executeCommand(t, module, ResolveActionCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: result.Workflow.Revision,
			TextValue:        &reviewedNameValue,
			Confirmed:        &confirmed,
		},
		IdempotencyKey: "review-tracker-name",
	})
	if dupeChecks != 1 || result.Projections == nil || result.Dupes == nil ||
		result.Projections.Projections[0].UploadReleaseName != reviewedName ||
		result.Projections.Projections[0].DuplicateCriteria.Name != automaticName ||
		result.Dupes.Results[0].UploadReleaseName != reviewedName ||
		result.Dupes.ProjectionSet != *result.Workflow.TrackerProjections ||
		*result.Workflow.Dupes == priorDupeRef ||
		len(result.Workflow.RequiredActions) != 1 ||
		result.Workflow.RequiredActions[0].Status != api.RequiredActionStatusResolved ||
		result.Workflow.Status != api.WorkflowStatusActive {
		t.Fatalf("reviewed tracker name result = %#v checks=%d", result, dupeChecks)
	}
	if _, err := module.private.Get(
		testOwnerID,
		result.Workflow.ID,
		dupePrivateResourceID(result.Dupes.ID),
		module.clock.Now().UTC(),
	); err != nil {
		t.Fatalf("load rebound duplicate evidence: %v", err)
	}

	confirmed = false
	action = result.Workflow.RequiredActions[0]
	result, err = module.Continue(context.Background(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       result.Workflow.ID,
			ExpectedRevision: result.Workflow.Revision,
		},
		IdempotencyKey: "unconfirm-tracker-name",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent:         api.WorkflowIntent{Interaction: api.InteractionModeInteractive},
		Answers: []api.RequiredActionAnswer{{
			ActionID:         action.ID,
			WorkflowRevision: result.Workflow.Revision,
			Confirmed:        &confirmed,
		}},
	})
	if err != nil {
		t.Fatalf("unconfirm tracker name: %v", err)
	}
	if dupeChecks != 1 || result.Projections == nil || result.Dupes == nil ||
		result.Projections.Projections[0].UploadReleaseName != automaticName ||
		result.Projections.Projections[0].UploadReady ||
		result.Dupes.Results[0].UploadReleaseName != automaticName ||
		len(result.Workflow.RequiredActions) != 1 ||
		result.Workflow.RequiredActions[0].Status != api.RequiredActionStatusPending ||
		result.Workflow.Status != api.WorkflowStatusBlocked ||
		len(result.Continuation.TrackerOutcomes) != 1 ||
		result.Continuation.TrackerOutcomes[0].Disposition != api.WorkflowDispositionNeedsAction {
		t.Fatalf("unconfirmed tracker name result = %#v checks=%d", result, dupeChecks)
	}

	confirmed = true
	action = result.Workflow.RequiredActions[0]
	result, err = module.Continue(context.Background(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority: &api.WorkflowAuthority{
			WorkflowID:       result.Workflow.ID,
			ExpectedRevision: result.Workflow.Revision,
		},
		IdempotencyKey: "reconfirm-tracker-name",
		Goal:           api.WorkflowGoalDuplicatesDecided,
		Intent:         api.WorkflowIntent{Interaction: api.InteractionModeInteractive},
		Answers: []api.RequiredActionAnswer{{
			ActionID:         action.ID,
			WorkflowRevision: result.Workflow.Revision,
			TextValue:        &reviewedNameValue,
			Confirmed:        &confirmed,
		}},
	})
	if err != nil {
		t.Fatalf("reconfirm tracker name: %v", err)
	}
	if dupeChecks != 1 || result.Projections == nil ||
		result.Projections.Projections[0].UploadReleaseName != reviewedName ||
		!result.Projections.Projections[0].UploadReady ||
		len(result.Workflow.RequiredActions) != 1 ||
		result.Workflow.RequiredActions[0].Status != api.RequiredActionStatusResolved ||
		result.Workflow.Status != api.WorkflowStatusActive ||
		len(result.Continuation.TrackerOutcomes) != 1 ||
		result.Continuation.TrackerOutcomes[0].Disposition == api.WorkflowDispositionNeedsAction {
		t.Fatalf("reconfirmed tracker name result = %#v checks=%d", result, dupeChecks)
	}
}

func TestModulePreflightPublishesImmutableAssessmentAndFinalizedProjectionRevision(t *testing.T) {
	t.Parallel()

	builder := trackerPreflightBuilderFunc(func(
		_ context.Context,
		_ api.UploadSubject,
		_ api.TrackerCatalogSnapshot,
		_ api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		results := make([]api.TrackerPreflightResult, 0, len(initial.Projections))
		finalized := append([]api.TrackerReleaseProjection(nil), initial.Projections...)
		for _, projection := range initial.Projections {
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint projection: %w", err)
			}
			results = append(results, api.TrackerPreflightResult{
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
			})
		}
		return api.TrackerPreflightAssessment{
			InputFingerprint: testFingerprint(t, "owned-preflight"),
			Results:          results,
			ExpiresAt:        now.Add(time.Hour),
		}, finalized, nil
	})
	module, repository := newTestModule(t, testPreparer(), WithTrackerPreflightBuilder(builder))
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-preflight"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testProjectionSet(t),
	})
	initialRef := *result.Workflow.TrackerProjections
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	if result.Preflight == nil || result.Projections == nil || result.Projections.Preflight == nil {
		t.Fatalf("preflight result = %#v", result)
	}
	if result.Preflight.ProjectionSet != initialRef || result.Projections.ID == initialRef.ID ||
		*result.Projections.Preflight != *result.Workflow.TrackerPreflight {
		t.Fatalf("preflight/final projection lineage = %#v", result)
	}
	state, err := repository.Load(context.Background(), testOwnerID, result.Workflow.ID)
	if err != nil {
		t.Fatalf("load preflight workflow: %v", err)
	}
	if state.Projections[initialRef.ID].Preflight != nil {
		t.Fatal("initial projection revision was mutated by preflight")
	}
}

func TestModuleUnattendedPreflightSkipsManualTrackerAndKeepsReadySibling(t *testing.T) {
	t.Parallel()

	builder := trackerPreflightBuilderFunc(func(
		_ context.Context,
		_ api.UploadSubject,
		_ api.TrackerCatalogSnapshot,
		_ api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		results := make([]api.TrackerPreflightResult, 0, len(initial.Projections))
		finalized := append([]api.TrackerReleaseProjection(nil), initial.Projections...)
		for index, projection := range initial.Projections {
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint projection: %w", err)
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
			if projection.TrackerID == "ALPHA" {
				action := api.RequiredAction{
					Kind:   legacyTrackerAuthActionKind,
					Prompt: "Authenticate this tracker.",
				}
				failure := api.WorkflowFailure{
					Failure: api.OperationFailure{
						Code:      api.OperationFailureTrackerAuthRequired,
						Operation: api.OperationKindDuplicateCheck,
						Message:   "Tracker authentication is required.",
						Recovery:  api.OperationRecoveryAuthenticateTrackers,
					},
					TrackerID: projection.TrackerID,
				}
				result.State = api.TrackerPreflightStateActionRequired
				result.AuthReady = false
				result.RequiredActions = []api.RequiredAction{action}
				result.Failures = []api.WorkflowFailure{failure}
				finalized[index].Readiness = api.ReadinessStatusBlocked
				finalized[index].DupeReady = false
				finalized[index].UploadReady = false
				finalized[index].RequiredActions = []api.RequiredAction{action}
				finalized[index].Failures = []api.WorkflowFailure{failure}
			}
			results = append(results, result)
		}
		return api.TrackerPreflightAssessment{
			InputFingerprint: testFingerprint(t, "unattended-preflight"),
			Results:          results,
			ExpiresAt:        now.Add(time.Hour),
		}, finalized, nil
	})
	module, _ := newTestModule(t, testPreparer(), WithTrackerPreflightBuilder(builder))
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-unattended-preflight"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testProjectionSet(t),
	})
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Interaction:      api.InteractionModeUnattended,
	})

	if result.Preflight == nil || result.Projections == nil {
		t.Fatalf("unattended preflight result = %#v", result)
	}
	if len(result.Workflow.RequiredActions) != 0 || len(result.Preflight.Results[0].RequiredActions) != 0 ||
		len(result.Projections.RequiredActions) != 0 {
		t.Fatalf("unattended preflight retained manual actions = %#v", result)
	}
	if result.Projections.Projections[0].TrackerID != "ALPHA" ||
		result.Projections.Projections[0].Readiness != api.ReadinessStatusIneligible {
		t.Fatalf("unattended ALPHA projection = %#v", result.Projections.Projections[0])
	}
	if !slices.ContainsFunc(result.Projections.Projections[0].PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == string(api.OperationFailureTrackerAuthRequired) &&
			decision.Decision == "ineligible" &&
			decision.Blocking &&
			decision.Message == "Tracker authentication is required."
	}) {
		t.Fatalf("unattended ALPHA diagnostics = %#v", result.Projections.Projections[0].PolicyDecisions)
	}
	if result.Projections.Projections[1].TrackerID != "BETA" ||
		result.Projections.Projections[1].Readiness != api.ReadinessStatusReady {
		t.Fatalf("unattended BETA projection = %#v", result.Projections.Projections[1])
	}
}

func TestModuleExpiredPreflightFailsClosedBeforeDuplicatePublication(t *testing.T) {
	t.Parallel()

	clock := &mutableClock{now: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)}
	module, _ := newTestModule(t, testPreparer(), WithClock(clock))
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-expired-preflight"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testProjectionSet(t),
	})
	module.trackerPreflight = readyPreflightBuilder(t)
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	clock.now = clock.now.Add(2 * time.Hour)
	_, err := executeTestPublicationRaw(module, dupeAssessmentPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testDupes(t),
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expired preflight dupe error = %v, want %v", err, ErrInvalidTransition)
	}
}

func TestModuleDuplicateAssessmentIsRetainedAndDecisionsDoNotRepeatSearch(t *testing.T) {
	t.Parallel()

	var checks int
	dupeBuilder := dupeAssessmentBuilderFunc(func(
		_ context.Context,
		_ api.DuplicateSubject,
		projections api.TrackerReleaseProjectionSet,
		_ api.TrackerPreflightAssessment,
		now time.Time,
		_ bool,
	) (api.DupeAssessment, any, error) {
		checks++
		results := make([]api.TrackerDupeAssessment, 0, len(projections.Projections))
		for index, projection := range projections.Projections {
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				return api.DupeAssessment{}, nil, fmt.Errorf("fingerprint dupe projection: %w", err)
			}
			result := api.TrackerDupeAssessment{
				TrackerID:             projection.TrackerID,
				UploadReleaseName:     projection.UploadReleaseName,
				ProjectionFingerprint: fingerprint,
				CriteriaFingerprint:   projection.CriteriaFingerprint,
				Criteria:              projection.DuplicateCriteria,
				Decision:              api.DupeDecisionNoMatch,
				Status:                api.StageStatusCompleted,
				CheckedAt:             now,
				FreshUntil:            now.Add(time.Hour),
			}
			if index == 0 {
				result.Matches = []api.DupeMatchProjection{{ID: "123", Name: "Example.Release.2026.ALPHA-GRP"}}
				result.Decision = api.DupeDecisionAccepted
			} else {
				result.Matches = []api.DupeMatchProjection{{
					ID:     "456",
					Name:   "Example.Release.2026.BETA-GRP",
					Reason: "in_client",
				}}
				result.Decision = api.DupeDecisionAccepted
			}
			results = append(results, result)
		}
		return api.DupeAssessment{
			InputFingerprint: testFingerprint(t, "dupe-owned"),
			Results:          results,
			ExpiresAt:        now.Add(time.Hour),
		}, struct{ Evidence string }{Evidence: "private"}, nil
	})
	var captures int
	descriptions := &descriptionBuilderFake{testing: t}
	uploadPlans := &uploadPlanBuilderFake{testing: t}
	mediaBuilder := mediaArtifactBuilderFunc(func(
		_ context.Context,
		_ api.ReleaseRef,
		projections api.TrackerReleaseProjectionSet,
		_ api.MediaCaptureInstructions,
		_ time.Time,
	) (api.MediaArtifactSet, any, error) {
		captures++
		requirements, err := mediaRequirementsFingerprint(projections.Projections)
		if err != nil {
			return api.MediaArtifactSet{}, nil, err
		}
		return api.MediaArtifactSet{
			CaptureFingerprint:      testFingerprint(t, "capture-owned"),
			RequirementsFingerprint: requirements,
			Artifacts: []api.MediaArtifact{{
				ID:       "artifact-1",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
			}},
			Status: api.StageStatusCompleted,
		}, struct{ Path string }{Path: "private.png"}, nil
	})
	module, _ := newTestModule(
		t,
		testPreparer(),
		WithTrackerPreflightBuilder(readyPreflightBuilder(t)),
		WithDupeAssessmentBuilder(dupeBuilder),
		WithMediaArtifactBuilder(mediaBuilder),
		WithDescriptionBuilder(descriptions),
		WithUploadPlanBuilder(uploadPlans),
	)
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-dupes"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         testProjectionSet(t),
	})
	result = executeCommand(t, module, PreflightTrackersCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	check := CheckDuplicatesCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		IdempotencyKey:   "check-once",
	}
	checked := executeCommand(t, module, check)
	if checked.Dupes == nil || checked.Dupes.Status != api.StageStatusCompleted ||
		checked.Dupes.Results[0].Decision != api.DupeDecisionAccepted || checks != 1 {
		t.Fatalf("checked dupes = %#v checks=%d", checked, checks)
	}
	repeated := executeCommand(t, module, check)
	if repeated.Dupes == nil || repeated.Dupes.ID != checked.Dupes.ID || checks != 1 {
		t.Fatalf("repeated dupes = %#v checks=%d", repeated, checks)
	}
	decided := executeCommand(t, module, DecideDuplicatesCommand{
		WorkflowID:       checked.Workflow.ID,
		ExpectedRevision: checked.Workflow.Revision,
		Decisions:        map[api.TrackerID]api.DupeDecision{"ALPHA": api.DupeDecisionIgnored},
	})
	if decided.Dupes == nil || decided.Dupes.Status != api.StageStatusCompleted ||
		decided.Dupes.Results[0].Decision != api.DupeDecisionIgnored || checks != 1 {
		t.Fatalf("decided dupes = %#v checks=%d", decided, checks)
	}
	_, err := module.Execute(context.Background(), testOwnerID, DecideDuplicatesCommand{
		WorkflowID:       decided.Workflow.ID,
		ExpectedRevision: decided.Workflow.Revision,
		Decisions:        map[api.TrackerID]api.DupeDecision{"BETA": api.DupeDecisionIgnored},
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("in-client duplicate override error = %v, want %v", err, ErrInvalidTransition)
	}
	capture := CaptureMediaCommand{
		WorkflowID:       decided.Workflow.ID,
		ExpectedRevision: decided.Workflow.Revision,
		Instructions:     api.MediaCaptureInstructions{ScreenshotCount: 1, Purpose: api.ScreenshotPurposeFinal},
		IdempotencyKey:   "capture-once",
	}
	captured := executeCommand(t, module, capture)
	if captured.Media == nil || captured.Media.ReleaseRef.SourcePath == "" || captures != 1 {
		t.Fatalf("captured media = %#v captures=%d", captured, captures)
	}
	repeatedCapture := executeCommand(t, module, capture)
	if repeatedCapture.Media == nil || repeatedCapture.Media.ID != captured.Media.ID || captures != 1 {
		t.Fatalf("repeated media = %#v captures=%d", repeatedCapture, captures)
	}
	generated := executeCommand(t, module, GenerateDescriptionsCommand{
		WorkflowID:       captured.Workflow.ID,
		ExpectedRevision: captured.Workflow.Revision,
		Instructions: api.DescriptionInstructions{
			TemplateVersion: "v1",
		},
		IdempotencyKey: "descriptions-first",
	})
	if generated.Descriptions == nil || generated.Descriptions.Media == nil || descriptions.builds != 1 {
		t.Fatalf("generated descriptions = %#v builds=%d", generated, descriptions.builds)
	}
	reused := executeCommand(t, module, GenerateDescriptionsCommand{
		WorkflowID:       generated.Workflow.ID,
		ExpectedRevision: generated.Workflow.Revision,
		Instructions: api.DescriptionInstructions{
			TemplateVersion: "v1",
		},
		IdempotencyKey: "descriptions-compatible",
	})
	if reused.Descriptions == nil || reused.Descriptions.ID != generated.Descriptions.ID ||
		reused.Descriptions.Revision != generated.Descriptions.Revision || descriptions.builds != 1 {
		t.Fatalf("reused descriptions = %#v builds=%d", reused, descriptions.builds)
	}
	current, err := module.Current(context.Background(), testOwnerID, reused.Workflow.ID)
	if err != nil {
		t.Fatalf("query current workflow snapshots: %v", err)
	}
	if current.Descriptions == nil || current.Descriptions.ID != generated.Descriptions.ID || current.Media == nil || current.Dupes == nil {
		t.Fatalf("current workflow snapshots = %#v", current)
	}
	dryRunCommand := DryRunUploadsCommand{
		WorkflowID:       reused.Workflow.ID,
		ExpectedRevision: reused.Workflow.Revision,
		NoSeed:           true,
		IdempotencyKey:   "upload-dry-run",
	}
	dryRun := executeCommand(t, module, dryRunCommand)
	if dryRun.DryRun == nil || dryRun.DryRun.Status != api.StageStatusCompleted || len(dryRun.DryRun.Reports) != 1 ||
		dryRun.DryRun.Reports[0].ClientInjection.Status != api.StageStatusSkipped || uploadPlans.builds != 1 {
		t.Fatalf("direct dry run = %#v builds=%d", dryRun.DryRun, uploadPlans.builds)
	}
	repeatedDryRun := executeCommand(t, module, dryRunCommand)
	if repeatedDryRun.DryRun == nil || repeatedDryRun.DryRun.ID != dryRun.DryRun.ID || uploadPlans.builds != 1 {
		t.Fatalf("idempotent direct dry run = %#v builds=%d", repeatedDryRun, uploadPlans.builds)
	}
	reviewedExecution := uploadPlans.execution
	reviewedUpload := executeCommand(t, module, ExecuteUploadsCommand{
		WorkflowID:       repeatedDryRun.Workflow.ID,
		ExpectedRevision: repeatedDryRun.Workflow.Revision,
		NoSeed:           true,
		IdempotencyKey:   "upload-exact-reviewed-plan",
	})
	if reviewedUpload.UploadResult == nil || reviewedUpload.UploadResult.Status != api.StageStatusCompleted ||
		reviewedUpload.UploadResult.InputFingerprint != repeatedDryRun.DryRun.InputFingerprint || uploadPlans.builds != 1 ||
		reviewedExecution.executions != 1 {
		t.Fatalf("exact reviewed upload = %#v builds=%d executions=%d", reviewedUpload.UploadResult, uploadPlans.builds, reviewedExecution.executions)
	}
	staleDescriptions := *reviewedUpload.Workflow.Descriptions
	savedDescription := executeCommand(t, module, SaveDescriptionOverrideCommand{
		WorkflowID:       reviewedUpload.Workflow.ID,
		ExpectedRevision: reviewedUpload.Workflow.Revision,
		Descriptions:     staleDescriptions,
		GroupKey:         "alpha",
		Source:           "Edited alpha description.",
		IdempotencyKey:   "save-alpha-description",
	})
	if savedDescription.Descriptions == nil || savedDescription.Workflow.DryRun != nil ||
		len(savedDescription.Descriptions.Descriptions) != 1 ||
		savedDescription.Descriptions.Descriptions[0].Source != "Edited alpha description." {
		t.Fatalf("saved exact description group = %#v workflow=%#v", savedDescription.Descriptions, savedDescription.Workflow)
	}
	_, err = module.Execute(context.Background(), testOwnerID, ResetDescriptionOverrideCommand{
		WorkflowID:       savedDescription.Workflow.ID,
		ExpectedRevision: savedDescription.Workflow.Revision,
		Descriptions:     staleDescriptions,
		GroupKey:         "alpha",
		IdempotencyKey:   "reset-stale-alpha-description",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale description revision error = %v, want %v", err, ErrInvalidTransition)
	}
	resetDescription := executeCommand(t, module, ResetDescriptionOverrideCommand{
		WorkflowID:       savedDescription.Workflow.ID,
		ExpectedRevision: savedDescription.Workflow.Revision,
		Descriptions:     *savedDescription.Workflow.Descriptions,
		GroupKey:         "alpha",
		IdempotencyKey:   "reset-alpha-description",
	})
	if resetDescription.Descriptions == nil || len(resetDescription.Descriptions.Descriptions) != 1 ||
		resetDescription.Descriptions.Descriptions[0].Source != "Example description for alpha." || descriptions.builds != 3 {
		t.Fatalf("reset exact description group = %#v builds=%d", resetDescription, descriptions.builds)
	}
	uploadPlans.failed = map[api.TrackerID]bool{"ALPHA": true}
	execute := ExecuteUploadsCommand{
		WorkflowID:       resetDescription.Workflow.ID,
		ExpectedRevision: resetDescription.Workflow.Revision,
		NoSeed:           true,
		IdempotencyKey:   "upload-direct-once",
	}
	executed := executeCommand(t, module, execute)
	failedExecution := uploadPlans.execution
	if executed.UploadResult == nil || executed.UploadResult.Status != api.StageStatusFailed || failedExecution.executions != 1 ||
		uploadPlans.builds != 2 {
		t.Fatalf("direct upload = %#v executions=%d builds=%d", executed, failedExecution.executions, uploadPlans.builds)
	}
	retriedExecution := executeCommand(t, module, execute)
	if retriedExecution.UploadResult == nil || retriedExecution.UploadResult.ID != executed.UploadResult.ID ||
		failedExecution.executions != 1 || uploadPlans.builds != 2 {
		t.Fatalf("idempotent direct upload = %#v executions=%d", retriedExecution, failedExecution.executions)
	}
	uploadPlans.failed = nil
	retry := RetryFailedUploadsCommand{
		WorkflowID:       executed.Workflow.ID,
		ExpectedRevision: executed.Workflow.Revision,
		Retry: api.FailedTrackerRetryRef{
			Result:     *executed.Workflow.UploadResult,
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		IdempotencyKey: "retry-failed-upload",
	}
	retried := executeCommand(t, module, retry)
	if retried.UploadResult == nil || retried.UploadResult.Status != api.StageStatusCompleted ||
		len(retried.UploadResult.Results) != 1 || retried.UploadResult.Results[0].TrackerID != "ALPHA" ||
		uploadPlans.builds != 3 || uploadPlans.execution.executions != 1 ||
		len(uploadPlans.options[2].TrackerIDs) != 1 || uploadPlans.options[2].TrackerIDs[0] != "ALPHA" {
		t.Fatalf("failed-only retry = %#v builds=%d options=%#v", retried, uploadPlans.builds, uploadPlans.options)
	}
	repeatedRetry := executeCommand(t, module, retry)
	if repeatedRetry.UploadResult == nil || repeatedRetry.UploadResult.ID != retried.UploadResult.ID ||
		uploadPlans.builds != 3 || uploadPlans.execution.executions != 1 {
		t.Fatalf("idempotent failed-only retry = %#v builds=%d", repeatedRetry, uploadPlans.builds)
	}
	_, err = module.Execute(context.Background(), testOwnerID, RetryFailedUploadsCommand{
		WorkflowID:       retried.Workflow.ID,
		ExpectedRevision: retried.Workflow.Revision,
		Retry: api.FailedTrackerRetryRef{
			Result:     *retried.Workflow.UploadResult,
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		IdempotencyKey: "retry-successful-upload",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("successful tracker retry error = %v, want %v", err, ErrInvalidTransition)
	}
	_, err = module.Execute(context.Background(), testOwnerID, ExecuteUploadsCommand{
		WorkflowID:       retried.Workflow.ID,
		ExpectedRevision: retried.Workflow.Revision,
		IdempotencyKey:   "repeat-upload-after-result",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("repeat direct upload error = %v, want %v", err, ErrInvalidTransition)
	}
}

func TestClientInjectionFailureCannotRetryTrackerSubmission(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	fingerprint := testFingerprint(t, "client-injection-only-failure")
	prior := api.UploadResult{
		ID:               "upload-result-1",
		WorkflowID:       "workflow-client-retry",
		Revision:         1,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1},
		Dupes:            api.DupeAssessmentRef{ID: "dupes-1", Revision: 1},
		Media:            api.MediaArtifactSetRef{ID: "media-1", Revision: 1},
		Descriptions:     api.DescriptionSetRef{ID: "descriptions-1", Revision: 1},
		InputFingerprint: fingerprint,
		Results: []api.UploadTrackerResult{{
			TrackerID:              "ALPHA",
			Status:                 api.StageStatusPartial,
			SubmissionStatus:       api.StageStatusCompleted,
			ClientInjectionStatus:  api.StageStatusFailed,
			ClientInjectionMessage: "Exact-torrent client injection failed.",
			ClientFailureCode:      api.OperationFailureClientInjection,
			RemoteID:               "123",
			Failures: []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureClientInjection,
					Operation: api.OperationKindClientInjection,
					Message:   "Exact-torrent client injection failed.",
					Recovery:  api.OperationRecoveryRetry,
				},
				TrackerID: "ALPHA",
				Resource:  "upload:ALPHA",
			}},
		}},
		Status:    api.StageStatusPartial,
		CreatedAt: now,
	}
	if err := prior.Validate(); err != nil {
		t.Fatalf("validate prior client failure: %v", err)
	}
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:                 prior.WorkflowID,
			Revision:           1,
			TrackerProjections: &prior.ProjectionSet,
			Dupes:              &prior.Dupes,
			Media:              &prior.Media,
			Descriptions:       &prior.Descriptions,
			UploadResult:       &api.UploadResultRef{ID: prior.ID, Revision: prior.Revision},
		},
		UploadResults: map[api.UploadResultID]api.UploadResult{prior.ID: prior},
	}
	uploadPlans := &uploadPlanBuilderFake{testing: t}
	module := &Module{uploadPlanBuilder: uploadPlans}
	_, err := module.retryFailedUploads(
		context.Background(),
		testOwnerID,
		&state,
		2,
		now.Add(time.Minute),
		RetryFailedUploadsCommand{
			Retry: api.FailedTrackerRetryRef{
				Result:     *state.Workflow.UploadResult,
				TrackerIDs: []api.TrackerID{"ALPHA"},
			},
		},
	)
	if !errors.Is(err, ErrInvalidTransition) || uploadPlans.builds != 0 {
		t.Fatalf("client-only tracker retry error=%v builds=%d", err, uploadPlans.builds)
	}
}

func TestRetryClientInjectionPreservesCompletedSubmission(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	fingerprint := testFingerprint(t, "retry-client-injection")
	prior := api.UploadResult{
		ID:               "upload-result-1",
		WorkflowID:       "workflow-client-retry",
		Revision:         1,
		ProjectionSet:    api.TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1},
		Dupes:            api.DupeAssessmentRef{ID: "dupes-1", Revision: 1},
		Media:            api.MediaArtifactSetRef{ID: "media-1", Revision: 1},
		Descriptions:     api.DescriptionSetRef{ID: "descriptions-1", Revision: 1},
		InputFingerprint: fingerprint,
		Results: []api.UploadTrackerResult{{
			TrackerID:              "ALPHA",
			Status:                 api.StageStatusPartial,
			SubmissionStatus:       api.StageStatusCompleted,
			ClientInjectionStatus:  api.StageStatusFailed,
			ClientInjectionMessage: "Exact-torrent client injection failed.",
			ClientFailureCode:      api.OperationFailureClientInjection,
			RemoteID:               "123",
			Failures: []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureClientInjection,
					Operation: api.OperationKindClientInjection,
					Message:   "Exact-torrent client injection failed.",
					Recovery:  api.OperationRecoveryRetry,
				},
				TrackerID: "ALPHA",
				Resource:  "upload:ALPHA",
			}},
		}},
		Status:    api.StageStatusPartial,
		CreatedAt: now,
	}
	if err := prior.Validate(); err != nil {
		t.Fatalf("validate prior client failure: %v", err)
	}
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:                 prior.WorkflowID,
			Revision:           1,
			TrackerProjections: &prior.ProjectionSet,
			Dupes:              &prior.Dupes,
			Media:              &prior.Media,
			Descriptions:       &prior.Descriptions,
			UploadResult:       &api.UploadResultRef{ID: prior.ID, Revision: prior.Revision},
		},
		Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{
			prior.ProjectionSet.ID: {ID: prior.ProjectionSet.ID, Revision: prior.ProjectionSet.Revision},
		},
		Dupes: map[api.DupeAssessmentID]api.DupeAssessment{
			prior.Dupes.ID: {ID: prior.Dupes.ID, Revision: prior.Dupes.Revision},
		},
		Media: map[api.MediaArtifactSetID]api.MediaArtifactSet{
			prior.Media.ID: {ID: prior.Media.ID, Revision: prior.Media.Revision},
		},
		Descriptions: map[api.DescriptionSetID]api.DescriptionSet{
			prior.Descriptions.ID: {ID: prior.Descriptions.ID, Revision: prior.Descriptions.Revision},
		},
		UploadResults: map[api.UploadResultID]api.UploadResult{prior.ID: prior},
	}
	authority := RegisteredArtifactAuthority{
		ClientSubject: api.ClientSubject{SourcePath: `C:\media\Example.Release.2026.1080p-GRP.mkv`},
		Torrents: map[api.TrackerID]api.TorrentResult{
			"ALPHA": {
				Tracker: "ALPHA",
				Path:    `C:\torrents\Example.Release.2026.1080p-GRP.ALPHA.torrent`,
			},
		},
	}
	privateStore := NewMemoryPrivateResourceStore()
	if err := privateStore.Put(
		testOwnerID,
		prior.WorkflowID,
		registeredArtifactAuthorityPrivateResourceID(prior.ID),
		authority,
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("retain registered artifact authority: %v", err)
	}
	uploadPlans := &uploadPlanBuilderFake{testing: t}
	module := &Module{
		private:           privateStore,
		uploadPlanBuilder: uploadPlans,
		logger:            api.NopLogger{},
		ids:               &sequenceIDGenerator{},
	}
	result, err := module.retryClientInjections(
		context.Background(),
		testOwnerID,
		&state,
		2,
		now.Add(time.Minute),
		RetryClientInjectionsCommand{
			Retry: api.ClientInjectionRetryRef{
				Result:     *state.Workflow.UploadResult,
				TrackerIDs: []api.TrackerID{"ALPHA"},
			},
		},
	)
	if err != nil {
		t.Fatalf("retry client injection: %v", err)
	}
	if result.UploadResult == nil || result.UploadResult.Status != api.StageStatusCompleted ||
		len(result.UploadResult.Results) != 1 {
		t.Fatalf("client injection retry result = %#v", result.UploadResult)
	}
	trackerResult := result.UploadResult.Results[0]
	if trackerResult.RemoteID != "123" ||
		trackerResult.SubmissionStatus != api.StageStatusCompleted ||
		trackerResult.ClientInjectionStatus != api.StageStatusCompleted ||
		!trackerResult.ClientInjected ||
		len(trackerResult.Failures) != 0 ||
		uploadPlans.builds != 0 ||
		uploadPlans.clientRetries != 1 {
		t.Fatalf(
			"client injection retry tracker result=%#v builds=%d retries=%d",
			trackerResult,
			uploadPlans.builds,
			uploadPlans.clientRetries,
		)
	}
}

func TestModuleCancellationDoesNotCommit(t *testing.T) {
	t.Parallel()

	var cancel context.CancelFunc
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
			cancel()
			return api.PrepareResult{}, ctx.Err()
		},
		DisplayFunc: func(context.Context, api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{}, errors.New("display must not run")
		},
	}
	module, _ := newTestModule(t, preparer)
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-cancel"})
	ctx, cancelContext := context.WithCancel(context.Background())
	cancel = cancelContext
	_, err := module.Execute(ctx, testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("prepare cancellation error = %v, want %v", err, context.Canceled)
	}
	workflow, queryErr := module.Workflow(context.Background(), testOwnerID, created.Workflow.ID)
	if queryErr != nil {
		t.Fatalf("query canceled workflow: %v", queryErr)
	}
	if workflow.Revision != created.Workflow.Revision || workflow.Release != nil {
		t.Fatalf("canceled command committed state: %#v", workflow)
	}
}

func TestModuleDurableOperationProgressDoesNotAdvanceWorkflowRevision(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	started := make(chan struct{})
	release := make(chan struct{})
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			api.EmitPreparationProgress(ctx, api.NewPreparationProgressUpdate(
				api.PreparationPhaseSourceInspection,
				api.PreparationProgressCompleted,
				"api_key=secret-value C:\\private\\Example.Release.2026.mkv",
			))
			api.EmitPreparationProgress(ctx, api.NewPreparationProgressUpdate(
				api.PreparationPhaseExternalIdentity,
				api.PreparationProgressCompleted,
				"External identity ready.",
			))
			close(started)
			select {
			case <-ctx.Done():
				return api.PrepareResult{}, fmt.Errorf("wait for operation release: %w", ctx.Err())
			case <-release:
			}
			return base.Prepare(ctx, input)
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	module, _ := newTestModule(t, preparer)
	created := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	command := PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "prepare-operation",
	}
	operation, err := module.Start(context.Background(), testOwnerID, command)
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	if operation.Status != api.StageStatusQueued || operation.Sequence != 1 || operation.Revision != created.Workflow.Revision {
		t.Fatalf("queued operation = %#v", operation)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not start")
	}
	progress := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusRunning && status.Sequence >= 4 && len(status.Items) > 1
	})
	if progress.Revision != created.Workflow.Revision || progress.Progress == 0 {
		t.Fatalf("running operation progress = %#v", progress)
	}
	if strings.Contains(progress.Message, "secret-value") || strings.Contains(progress.Message, "Example.Release.2026.mkv") {
		t.Fatalf("operation progress was not sanitized: %q", progress.Message)
	}
	if !slices.ContainsFunc(progress.Events, func(event api.WorkflowEvent) bool {
		return event.ScopeID == string(api.PreparationPhaseExternalIdentity) && event.State == api.StageStatusCompleted
	}) {
		t.Fatalf("running operation omitted changed child event: %#v", progress.Events)
	}
	retainedEvents, err := module.OperationEvents(context.Background(), testOwnerID, created.Workflow.ID, operation.ID, 0, 1000)
	if err != nil {
		t.Fatalf("load retained operation events: %v", err)
	}
	for _, phase := range []api.PreparationProgressPhase{
		api.PreparationPhaseSourceInspection,
		api.PreparationPhaseExternalIdentity,
	} {
		if !slices.ContainsFunc(retainedEvents, func(event api.WorkflowEvent) bool {
			return event.ScopeID == string(phase) && event.State == api.StageStatusCompleted
		}) {
			t.Fatalf("retained operation events omitted %s: %#v", phase, retainedEvents)
		}
	}
	current, err := module.Current(context.Background(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("current workflow while running: %v", err)
	}
	if current.Workflow.Revision != created.Workflow.Revision || current.Operation == nil || current.Operation.ID != operation.ID {
		t.Fatalf("running workflow snapshot = %#v", current)
	}
	repeated, err := module.Start(context.Background(), testOwnerID, command)
	if err != nil {
		t.Fatalf("repeat operation: %v", err)
	}
	if repeated.ID != operation.ID {
		t.Fatalf("idempotent operation id = %s, want %s", repeated.ID, operation.ID)
	}
	if _, err := module.Execute(context.Background(), testOwnerID, ReplaceFactInstructionsCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Instructions:     api.ReleaseFactInstructions{},
	}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("concurrent mutation error = %v, want %v", err, ErrOperationConflict)
	}
	close(release)
	terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCompleted
	})
	if terminal.Progress != 100 || terminal.ResultRevision != created.Workflow.Revision+1 || terminal.CompletedAt == nil {
		t.Fatalf("terminal operation = %#v", terminal)
	}
	if terminal.Result == nil || terminal.Result.Kind != api.WorkflowOperationResultRelease ||
		terminal.Result.WorkflowRevision != terminal.ResultRevision || terminal.Result.RefRevision != terminal.ResultRevision ||
		terminal.Result.RefID == "" {
		t.Fatalf("terminal exact result = %#v", terminal.Result)
	}
	terminalRepeat, err := module.Start(context.Background(), testOwnerID, command)
	if err != nil {
		t.Fatalf("repeat terminal operation: %v", err)
	}
	if terminalRepeat.ID != operation.ID || terminalRepeat.Status != api.StageStatusCompleted {
		t.Fatalf("terminal idempotent operation = %#v", terminalRepeat)
	}
}

func TestModuleDoesNotPublishTerminalOperationBeforeDurableWorkCompletion(t *testing.T) {
	t.Parallel()

	repository := &blockingCompleteWorkRepository{
		MemoryRepository: NewMemoryRepository(),
		completeStarted:  make(chan struct{}),
		releaseComplete:  make(chan struct{}),
	}
	defer func() {
		select {
		case <-repository.releaseComplete:
		default:
			close(repository.releaseComplete)
		}
	}()
	module, err := New(repository, NewMemoryPrivateResourceStore(), testPreparer())
	if err != nil {
		t.Fatalf("new module: %v", err)
	}
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-terminal-cleanup"})
	operation, err := module.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "prepare-terminal-cleanup",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	select {
	case <-repository.completeStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not begin durable completion")
	}
	record, err := repository.LoadOperation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("load stored operation before durable completion: %v", err)
	}
	if isTerminalProgressStatus(record.Status.Status) {
		t.Fatalf("stored operation became terminal before durable completion: %#v", record.Status)
	}
	status, err := module.Operation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("poll operation before durable completion: %v", err)
	}
	if isTerminalProgressStatus(status.Status) {
		t.Fatalf("terminal operation published before durable completion: %#v", status)
	}

	close(repository.releaseComplete)
	status = waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(candidate api.WorkflowOperationStatus) bool {
		return candidate.Status == api.StageStatusCompleted
	})
	if status.Status != api.StageStatusCompleted {
		t.Fatalf("published operation status = %s, want %s", status.Status, api.StageStatusCompleted)
	}
}

func TestModuleResumesExpiredCheckpointSafeOperationFromPrivateCommandCapsule(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 6, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	vaultRoot := t.TempDir()
	vaultA, err := NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new first private vault: %v", err)
	}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	var calls atomic.Int32
	preparer, ok := testPreparer().(ReleasePreparerFunc)
	if !ok {
		t.Fatalf("test preparer type = %T, want ReleasePreparerFunc", testPreparer())
	}
	prepare := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			defer close(firstDone)
		}
		return prepare(ctx, input)
	}
	moduleA, err := New(
		repository,
		vaultA,
		preparer,
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-a"),
	)
	if err != nil {
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{
		WorkflowID:     "workflow-resume",
		IdempotencyKey: "create-resume",
	})
	operation, err := moduleA.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP",
		},
		IdempotencyKey: "prepare-resume",
	})
	if err != nil {
		t.Fatalf("start first operation: %v", err)
	}
	<-firstStarted
	moduleA.operationWorkersMu.Lock()
	firstWorker := moduleA.operationWorkers[operation.ID]
	moduleA.operationWorkersMu.Unlock()

	vaultB, err := NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new restarted private vault: %v", err)
	}
	moduleB, err := New(
		repository,
		vaultB,
		preparer,
		WithClock(fixedClock{now: now.Add(2 * workflowWorkLeaseTTL)}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	if _, err := moduleB.Current(context.Background(), testOwnerID, created.Workflow.ID); err != nil {
		t.Fatalf("trigger operation recovery: %v", err)
	}
	var resumed api.WorkflowOperationStatus
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resumed, err = moduleB.Operation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
		if err == nil && resumed.Status == api.StageStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("load resumed operation: %v", err)
	}
	if resumed.Status != api.StageStatusCompleted || resumed.ResultRevision != 2 {
		t.Fatalf("resumed operation = %#v", resumed)
	}
	current, err := moduleB.Current(context.Background(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("load resumed workflow: %v", err)
	}
	if current.Workflow.Revision != 2 || current.Release == nil {
		t.Fatalf("resumed workflow = %#v", current)
	}
	if calls.Load() != 2 {
		t.Fatalf("preparation calls = %d, want interrupted and resumed attempts", calls.Load())
	}
	if _, err := vaultB.Get(
		testOwnerID,
		created.Workflow.ID,
		operationCommandResourceID(operation.ID),
		now.Add(2*workflowWorkLeaseTTL),
	); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("completed operation command resource error = %v", err)
	}

	close(releaseFirst)
	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("first preparation attempt did not exit")
	}
	if firstWorker.done != nil {
		select {
		case <-firstWorker.done:
		case <-time.After(10 * time.Second):
			t.Fatal("first operation worker did not exit")
		}
	}
	retained, err := moduleB.Operation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("reload resumed operation: %v", err)
	}
	if retained.Status != api.StageStatusCompleted {
		t.Fatalf("old worker overwrote resumed result: %#v", retained)
	}
}

func TestModuleInterruptsRecoveredOperationWhenWorkflowAuthorityAdvanced(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 6, 15, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	vaultRoot := t.TempDir()
	vaultA, err := NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new first private vault: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	base := testPreparer()
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			select {
			case <-ctx.Done():
				return api.PrepareResult{}, fmt.Errorf("wait for stale operation release: %w", ctx.Err())
			case <-release:
			}
			return base.Prepare(ctx, input)
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	moduleA, err := New(
		repository,
		vaultA,
		preparer,
		WithClock(fixedClock{now: now}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-stale-a"),
	)
	if err != nil {
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{
		WorkflowID:     "workflow-stale-recovery",
		IdempotencyKey: "create-stale-recovery",
	})
	operation, err := moduleA.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input: api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP"),
		},
		IdempotencyKey: "prepare-stale-recovery",
	})
	if err != nil {
		t.Fatalf("start first operation: %v", err)
	}
	<-started
	moduleA.operationWorkersMu.Lock()
	firstWorker := moduleA.operationWorkers[operation.ID]
	moduleA.operationWorkersMu.Unlock()

	state, err := repository.Load(context.Background(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("load workflow before authority advance: %v", err)
	}
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = now.Add(time.Second)
	if err := repository.Save(context.Background(), testOwnerID, created.Workflow.Revision, state); err != nil {
		t.Fatalf("advance workflow authority: %v", err)
	}

	vaultB, err := NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new restarted private vault: %v", err)
	}
	moduleB, err := New(
		repository,
		vaultB,
		preparer,
		WithClock(fixedClock{now: now.Add(2 * workflowWorkLeaseTTL)}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithProcessEpoch("epoch-stale-b"),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	recovered, err := moduleB.Operation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("recover stale operation: %v", err)
	}
	if recovered.Status != api.StageStatusInterrupted || len(recovered.Failures) != 1 ||
		recovered.Failures[0].Failure.Code != api.OperationFailureStaleReview {
		t.Fatalf("stale recovered operation = %#v", recovered)
	}
	if calls.Load() != 1 {
		t.Fatalf("preparation calls = %d, want stale operation not resumed", calls.Load())
	}

	close(release)
	if firstWorker.done != nil {
		select {
		case <-firstWorker.done:
		case <-time.After(10 * time.Second):
			t.Fatal("first stale operation worker did not exit")
		}
	}
}

func TestModulePublishesCompletedWorkCheckpointAfterRestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 6, 30, 0, 0, time.UTC)
	repository := &failOnceTerminalOperationSaveRepository{
		MemoryRepository: NewMemoryRepository(),
		saveFailed:       make(chan struct{}),
	}
	vaultRoot := t.TempDir()
	vaultA, err := NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new first private vault: %v", err)
	}
	moduleA, err := New(
		repository,
		vaultA,
		testPreparer(),
		WithClock(fixedClock{now: now}),
	)
	if err != nil {
		t.Fatalf("new first module: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{WorkflowID: "workflow-completed-checkpoint"})
	operation, err := moduleA.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "prepare-completed-checkpoint",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	select {
	case <-repository.saveFailed:
	case <-time.After(10 * time.Second):
		t.Fatal("terminal operation save did not fail")
	}
	stored, err := repository.LoadOperation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("load operation before restart: %v", err)
	}
	if !workflowOperationActive(stored.Status.Status) {
		t.Fatalf("operation status before restart = %s, want active", stored.Status.Status)
	}
	work, err := repository.LoadWork(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("load completed work before restart: %v", err)
	}
	if work.CompletedAt == nil {
		t.Fatalf("work before restart = %#v, want completed checkpoint", work)
	}

	vaultB, err := NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new restarted private vault: %v", err)
	}
	moduleB, err := New(
		repository,
		vaultB,
		testPreparer(),
		WithClock(fixedClock{now: now.Add(time.Second)}),
	)
	if err != nil {
		t.Fatalf("new restarted module: %v", err)
	}
	recovered, err := moduleB.Operation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("recover completed operation checkpoint: %v", err)
	}
	if recovered.Status != api.StageStatusCompleted || recovered.Result == nil ||
		recovered.Result.Kind != api.WorkflowOperationResultRelease {
		t.Fatalf("recovered completed operation = %#v", recovered)
	}
	if _, err := vaultB.Get(
		testOwnerID,
		created.Workflow.ID,
		operationCommandResourceID(operation.ID),
		now.Add(time.Second),
	); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("completed operation command capsule error = %v, want unavailable", err)
	}
}

func TestAcceptedCommandFingerprintIncludesWorkflowAuthority(t *testing.T) {
	t.Parallel()

	base := PrepareReleaseCommand{
		WorkflowID:       "workflow-authority-a",
		ExpectedRevision: 1,
		Input: api.PrepareInput{
			SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP"),
		},
		IdempotencyKey: "prepare-authority",
	}
	same := base
	differentWorkflow := base
	differentWorkflow.WorkflowID = "workflow-authority-b"
	differentRevision := base
	differentRevision.ExpectedRevision = 2

	baseFingerprint, err := acceptedCommandFingerprint(base)
	if err != nil {
		t.Fatalf("fingerprint base command: %v", err)
	}
	sameFingerprint, err := acceptedCommandFingerprint(same)
	if err != nil {
		t.Fatalf("fingerprint same command: %v", err)
	}
	workflowFingerprint, err := acceptedCommandFingerprint(differentWorkflow)
	if err != nil {
		t.Fatalf("fingerprint different workflow: %v", err)
	}
	revisionFingerprint, err := acceptedCommandFingerprint(differentRevision)
	if err != nil {
		t.Fatalf("fingerprint different revision: %v", err)
	}
	if baseFingerprint != sameFingerprint {
		t.Fatalf("same accepted command fingerprints differ: %q != %q", baseFingerprint, sameFingerprint)
	}
	if baseFingerprint == workflowFingerprint || baseFingerprint == revisionFingerprint {
		t.Fatalf("authority was omitted: base=%q workflow=%q revision=%q", baseFingerprint, workflowFingerprint, revisionFingerprint)
	}
}

func TestApplyWorkflowProgressConvergesAfterDuplicateAndStaleUpdates(t *testing.T) {
	t.Parallel()

	status := api.WorkflowOperationStatus{Status: api.StageStatusRunning}
	running := api.WorkflowProgressUpdate{
		Phase:     "duplicate_check",
		ItemID:    "BETA",
		Kind:      "tracker",
		Label:     "BETA",
		Status:    api.StageStatusRunning,
		Completed: 8,
		Total:     10,
		Message:   "Checking tracker.",
	}
	completed := running
	completed.Status = api.StageStatusCompleted
	completed.Completed = 10
	completed.Message = "Tracker checked."
	stale := running
	stale.Completed = 5

	applyWorkflowProgress(&status, running)
	applyWorkflowProgress(&status, completed)
	applyWorkflowProgress(&status, completed)
	applyWorkflowProgress(&status, stale)

	if status.Completed != 10 || status.Total != 10 || status.Progress != 100 {
		t.Fatalf("converged aggregate progress = %#v", status)
	}
	if len(status.Items) != 1 || status.Items[0].Status != api.StageStatusCompleted ||
		status.Items[0].Completed != 10 || status.Items[0].Message != "Tracker checked." ||
		status.Items[0].Phase != "duplicate_check" {
		t.Fatalf("converged item progress = %#v", status.Items)
	}
}

func TestReduceUploadDryRunReportsRetainsMixedAndSkippedOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		reports []api.TrackerDryRunReport
		status  api.StageStatus
		counts  [3]int
	}{
		{
			name: "all success",
			reports: []api.TrackerDryRunReport{
				{Status: api.StageStatusCompleted},
				{Status: api.StageStatusCompleted},
			},
			status: api.StageStatusCompleted,
			counts: [3]int{2, 0, 0},
		},
		{
			name: "mixed",
			reports: []api.TrackerDryRunReport{
				{Status: api.StageStatusCompleted},
				{Status: api.StageStatusFailed},
				{Status: api.StageStatusSkipped},
			},
			status: api.StageStatusPartial,
			counts: [3]int{1, 1, 1},
		},
		{
			name:    "all failed",
			reports: []api.TrackerDryRunReport{{Status: api.StageStatusFailed}},
			status:  api.StageStatusFailed,
			counts:  [3]int{0, 1, 0},
		},
		{
			name:    "all skipped",
			reports: []api.TrackerDryRunReport{{Status: api.StageStatusSkipped}},
			status:  api.StageStatusSkipped,
			counts:  [3]int{0, 0, 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			succeeded, failed, skipped, status := reduceUploadDryRunReports(test.reports)
			if status != test.status || [3]int{succeeded, failed, skipped} != test.counts {
				t.Fatalf("reduced outcome = %s [%d %d %d], want %s %v", status, succeeded, failed, skipped, test.status, test.counts)
			}
		})
	}
}

func TestModuleDurableOperationThrottlesIntermediateWrites(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			for completed := 1; completed < 100; completed++ {
				api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
					Phase:     "preparation",
					ItemID:    "source",
					Kind:      "preparation_phase",
					Label:     "Prepare source",
					Status:    api.StageStatusRunning,
					Completed: completed,
					Total:     100,
					Message:   "Preparing source.",
				})
			}
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:     "preparation",
				ItemID:    "source",
				Kind:      "preparation_phase",
				Label:     "Prepare source",
				Status:    api.StageStatusCompleted,
				Completed: 100,
				Total:     100,
				Message:   "Source prepared.",
			})
			return base.Prepare(ctx, input)
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	repository := &countingOperationRepository{MemoryRepository: NewMemoryRepository()}
	module, err := New(
		repository,
		NewMemoryPrivateResourceStore(),
		preparer,
		WithClock(fixedClock{now: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)}),
		WithIDGenerator(&sequenceIDGenerator{}),
	)
	if err != nil {
		t.Fatalf("new workflow module: %v", err)
	}
	created := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	operation, err := module.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "throttled-progress",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCompleted
	})
	if saves := repository.saves.Load(); saves > 6 {
		t.Fatalf("durable operation saves = %d, want at most 6 for 100 updates", saves)
	}
	if len(terminal.Items) != 1 || terminal.Items[0].Status != api.StageStatusCompleted || terminal.Items[0].Completed != 100 {
		t.Fatalf("terminal throttled progress = %#v", terminal)
	}
}

func TestModuleDurableOperationReporterPanicPreservesWorkflow(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:     "preparation",
				ItemID:    "source",
				Kind:      "preparation_phase",
				Label:     "Prepare source",
				Status:    api.StageStatusCompleted,
				Completed: 1,
				Total:     1,
				Message:   "Source prepared.",
			})
			return base.Prepare(ctx, input)
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	repository := &panicProgressOperationRepository{MemoryRepository: NewMemoryRepository()}
	module, err := New(
		repository,
		NewMemoryPrivateResourceStore(),
		preparer,
		WithClock(fixedClock{now: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)}),
		WithIDGenerator(&sequenceIDGenerator{}),
	)
	if err != nil {
		t.Fatalf("new workflow module: %v", err)
	}
	created := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	operation, err := module.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "reporter-panic",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCompleted
	})
	if !repository.panicked.Load() || terminal.ResultRevision != created.Workflow.Revision+1 {
		t.Fatalf("reporter panic result: panicked=%t terminal=%#v", repository.panicked.Load(), terminal)
	}
	current, err := module.Current(context.Background(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("current workflow after reporter panic: %v", err)
	}
	if current.Release == nil || current.Workflow.Revision != created.Workflow.Revision+1 {
		t.Fatalf("workflow after reporter panic = %#v", current)
	}
}

func TestModuleDurableOperationRetainsClassifiedFailure(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(context.Context, api.PrepareInput) (api.PrepareResult, error) {
			return api.PrepareResult{}, errors.New("private preparation cause")
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	module, _ := newTestModule(t, preparer, WithOperationErrorClassifier(func(operation api.OperationKind, err error) error {
		return api.NewOperationError(api.OperationFailure{
			Code:      api.OperationFailureInvalidSource,
			Operation: operation,
			Message:   "The source path is unavailable.",
			Recovery:  api.OperationRecoveryEditInput,
		}, err)
	}))
	created := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	operation, err := module.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "classified-failure",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusFailed
	})
	if len(terminal.Failures) != 1 || terminal.Failures[0].Failure.Code != api.OperationFailureInvalidSource ||
		terminal.Failures[0].Failure.Operation != api.OperationKindPreparation {
		t.Fatalf("terminal classified failure = %#v", terminal.Failures)
	}
}

func TestModuleOperationCancellationIsIdempotent(t *testing.T) {
	t.Parallel()

	base := testPreparer()
	started := make(chan struct{})
	preparer := ReleasePreparerFunc{
		PrepareFunc: func(ctx context.Context, _ api.PrepareInput) (api.PrepareResult, error) {
			close(started)
			<-ctx.Done()
			return api.PrepareResult{}, fmt.Errorf("wait for cancellation: %w", ctx.Err())
		},
		DisplayFunc:   base.ResolveDisplay,
		SubjectFunc:   base.ResolveUploadSubject,
		DuplicateFunc: base.ResolveDuplicateSubject,
	}
	module, _ := newTestModule(t, preparer)
	created := executeCommand(t, module, CreateWorkflowCommand{Instructions: api.ReleaseFactInstructions{}})
	operation, err := module.Start(context.Background(), testOwnerID, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "source"},
		IdempotencyKey:   "cancel-operation",
	})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not start")
	}
	if _, err := module.CancelOperation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID); err != nil {
		t.Fatalf("cancel operation: %v", err)
	}
	terminal := waitForWorkflowOperation(t, module, created.Workflow.ID, operation.ID, func(status api.WorkflowOperationStatus) bool {
		return status.Status == api.StageStatusCanceled
	})
	repeated, err := module.CancelOperation(context.Background(), testOwnerID, created.Workflow.ID, operation.ID)
	if err != nil {
		t.Fatalf("repeat cancel operation: %v", err)
	}
	if repeated.Sequence != terminal.Sequence || repeated.Status != api.StageStatusCanceled {
		t.Fatalf("idempotent cancel = %#v, terminal=%#v", repeated, terminal)
	}
}

func TestValidateDescriptionBuildUsesArtifactRequirementNotGroupPresence(t *testing.T) {
	t.Parallel()

	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:        "DESCRIPTION",
			DescriptionGroup: "unit3d",
			Artifacts:        api.TrackerArtifactRequirements{Description: true},
		},
		{
			TrackerID:        "SCREENSHOTS",
			DescriptionGroup: "unit3d",
			Artifacts:        api.TrackerArtifactRequirements{ScreenshotCount: 2},
		},
		{TrackerID: "NONE", DescriptionGroup: "unit3d"},
	}}
	snapshot := api.DescriptionSet{
		Descriptions: []api.RenderedDescription{{
			GroupKey:   "unit3d|img.example|global",
			TrackerIDs: []api.TrackerID{"DESCRIPTION"},
		}},
		Status: api.StageStatusCompleted,
	}
	if err := validateDescriptionBuild(projections, snapshot); err != nil {
		t.Fatalf("validate mixed-mode descriptions: %v", err)
	}
	skippedProjections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:        "DESCRIPTION",
			DescriptionGroup: "unit3d",
			Artifacts:        api.TrackerArtifactRequirements{Description: true},
		},
		{
			TrackerID:        "SKIPPED",
			DescriptionGroup: "skipped",
			Artifacts:        api.TrackerArtifactRequirements{Description: true},
		},
	}}
	skippedSnapshot := api.DescriptionSet{
		Descriptions: []api.RenderedDescription{{
			GroupKey:   "unit3d|img.example|global",
			TrackerIDs: []api.TrackerID{"DESCRIPTION"},
		}},
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "DESCRIPTION", Status: api.StageStatusCompleted},
			{
				TrackerID: "SKIPPED",
				Status:    api.StageStatusSkipped,
				Message:   "No suitable image host was available.",
			},
		},
		Status: api.StageStatusCompleted,
	}
	if err := validateDescriptionBuild(skippedProjections, skippedSnapshot); err != nil {
		t.Fatalf("validate skipped tracker description: %v", err)
	}

	snapshot.Descriptions[0].TrackerIDs = append(snapshot.Descriptions[0].TrackerIDs, "SCREENSHOTS")
	if err := validateDescriptionBuild(projections, snapshot); err == nil || !strings.Contains(err.Error(), "does not consume descriptions") {
		t.Fatalf("unexpected non-description tracker error = %v", err)
	}
}

func TestModuleMediaMutationUsesOpaqueIDsAndInvalidatesDownstream(t *testing.T) {
	t.Parallel()

	privateMedia := &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}
	mediaBuilder := mediaArtifactBuilderFunc(func(
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
			CaptureFingerprint:      testFingerprint(t, "mutable-media"),
			RequirementsFingerprint: requirements,
			Artifacts: []api.MediaArtifact{{
				ID:       "artifact-1",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
			}},
			Status: api.StageStatusCompleted,
		}, privateMedia, nil
	})
	module, _ := newTestModule(
		t,
		testPreparer(),
		WithTrackerPreflightBuilder(readyPreflightBuilder(t)),
		WithDupeAssessmentBuilder(dupeAssessmentBuilderFunc(func(
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
					return api.DupeAssessment{}, nil, fmt.Errorf("fingerprint synthetic dupe projection: %w", err)
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
				InputFingerprint: testFingerprint(t, "mutable-media-dupes"),
				Results:          results,
				ExpiresAt:        now.Add(time.Hour),
			}, struct{ Evidence string }{Evidence: "synthetic"}, nil
		})),
		WithMediaArtifactBuilder(mediaBuilder),
		WithDescriptionBuilder(&descriptionBuilderFake{testing: t}),
		WithUploadPlanBuilder(&uploadPlanBuilderFake{testing: t}),
	)
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-media-mutation"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	projectionSet := testProjectionSet(t)
	for index := range projectionSet.Projections {
		projectionSet.Projections[index].Artifacts.ScreenshotCount = 1
	}
	projectionSet.Projections[0].DescriptionGroup = "alpha"
	projectionSet.Projections[1].DescriptionGroup = "beta"
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         projectionSet,
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
	mediaRef := *result.Workflow.Media
	result = executeCommand(t, module, GenerateDescriptionsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	result = executeCommand(t, module, DryRunUploadsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})
	result = executeCommand(t, module, SaveDescriptionOverrideCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Descriptions:     *result.Workflow.Descriptions,
		GroupKey:         "alpha",
		Source:           "Edited alpha description.",
	})
	if result.Descriptions == nil || result.Workflow.DryRun != nil || len(result.Descriptions.Descriptions) != 2 ||
		result.Descriptions.Descriptions[0].Source != "Edited alpha description." ||
		result.Descriptions.Descriptions[1].Source != "Example description for beta." {
		t.Fatalf("description mutation did not preserve unrelated group: %#v", result.Descriptions)
	}
	result = executeCommand(t, module, ResetDescriptionOverrideCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Descriptions:     *result.Workflow.Descriptions,
		GroupKey:         "alpha",
	})
	if result.Descriptions == nil || len(result.Descriptions.Descriptions) != 2 ||
		result.Descriptions.Descriptions[0].Source != "Example description for alpha." ||
		result.Descriptions.Descriptions[1].Source != "Example description for beta." {
		t.Fatalf("description reset did not preserve unrelated group: %#v", result.Descriptions)
	}
	result = executeCommand(t, module, DryRunUploadsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
	})

	selection := SetMediaSelectionCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Media:            mediaRef,
		ArtifactIDs:      []api.PublicResourceID{"artifact-1"},
		Selected:         false,
		IdempotencyKey:   "unselect-artifact",
	}
	mutated := executeCommand(t, module, selection)
	if mutated.Media == nil || mutated.Media.Artifacts[0].Selected || mutated.Media.Status != api.StageStatusBlocked {
		t.Fatalf("mutated media = %#v", mutated.Media)
	}
	if mutated.Workflow.Descriptions != nil || mutated.Workflow.DryRun != nil || mutated.Workflow.UploadResult != nil {
		t.Fatalf("media mutation retained downstream authority: %#v", mutated.Workflow)
	}
	content, err := module.MediaArtifact(
		context.Background(),
		testOwnerID,
		mutated.Workflow.ID,
		*mutated.Workflow.Media,
		"artifact-1",
	)
	if err != nil {
		t.Fatalf("read current media artifact: %v", err)
	}
	_ = content.Body.Close()
	if _, err := module.MediaArtifact(context.Background(), testOwnerID, mutated.Workflow.ID, mediaRef, "artifact-1"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale media read error = %v, want revision conflict", err)
	}
	if _, err := module.MediaArtifact(context.Background(), "different-owner", mutated.Workflow.ID, *mutated.Workflow.Media, "artifact-1"); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign-owner media read error = %v, want not found", err)
	}

	deletion := DeleteMediaArtifactsCommand{
		WorkflowID:       mutated.Workflow.ID,
		ExpectedRevision: mutated.Workflow.Revision,
		Media:            *mutated.Workflow.Media,
		ArtifactIDs:      []api.PublicResourceID{"artifact-1"},
		IdempotencyKey:   "delete-artifact",
	}
	requireArtifactRetained := func(t *testing.T) {
		t.Helper()
		state, err := module.repository.Load(context.Background(), testOwnerID, mutated.Workflow.ID)
		if err != nil {
			t.Fatalf("load durable workflow state: %v", err)
		}
		if state.Workflow.Revision != mutated.Workflow.Revision || state.Workflow.Media == nil {
			t.Fatalf("durable workflow advanced past failed delete: %#v", state.Workflow)
		}
		media, ok := state.Media[state.Workflow.Media.ID]
		if !ok || len(media.Artifacts) != 1 || media.Artifacts[0].ID != "artifact-1" {
			t.Fatalf("durable media lost the artifact after failed delete: %#v", media.Artifacts)
		}
	}
	privateMedia.stats.commitErr = errors.New("synthetic media commit failure")
	if _, err := module.Execute(context.Background(), testOwnerID, deletion); err == nil ||
		!strings.Contains(err.Error(), "synthetic media commit failure") {
		t.Fatalf("failed media finalization error = %v", err)
	}
	requireArtifactRetained(t)
	privateMedia.stats.commitErr = nil
	module.repository = &failOnceSaveRepository{Repository: module.repository}
	if _, err := module.Execute(context.Background(), testOwnerID, deletion); err == nil ||
		!strings.Contains(err.Error(), "synthetic repository save failure") {
		t.Fatalf("failed media save error = %v", err)
	}
	if privateMedia.stats.deletions != 1 {
		t.Fatalf("staged deletion did not commit before the snapshot save: %d", privateMedia.stats.deletions)
	}
	requireArtifactRetained(t)
	retry := deletion
	retry.IdempotencyKey = "delete-artifact-retry"
	deleted := executeCommand(t, module, retry)
	if deleted.Media == nil || len(deleted.Media.Artifacts) != 0 || privateMedia.stats.deletions != 2 {
		t.Fatalf("deleted media = %#v deletions=%d", deleted.Media, privateMedia.stats.deletions)
	}
	if privateMedia.stats.staged != 3 {
		t.Fatalf("staged deletion attempts = %d, want 3", privateMedia.stats.staged)
	}
	replayed := executeCommand(t, module, retry)
	if replayed.Media == nil || replayed.Media.ID != deleted.Media.ID || privateMedia.stats.deletions != 2 {
		t.Fatalf("idempotent delete = %#v deletions=%d", replayed.Media, privateMedia.stats.deletions)
	}
}

// readyDupeBuilder returns a dupe assessment builder that clears every selected
// tracker, so tests can advance past duplicate checking to media commands.
func readyDupeBuilder(t *testing.T) dupeAssessmentBuilderFunc {
	t.Helper()
	return func(
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
				return api.DupeAssessment{}, nil, fmt.Errorf("fingerprint synthetic dupe projection: %w", err)
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
			InputFingerprint: testFingerprint(t, "ready-dupes"),
			Results:          results,
			ExpiresAt:        now.Add(time.Hour),
		}, struct{ Evidence string }{Evidence: "synthetic"}, nil
	}
}

// TestReorderMediaArtifactsChangesOrderNotCaptureIndex pins the split that lets
// callers reorder final media without disturbing capture slots: Order owns
// display position, Index owns the capture slot, and Kind separates screenshots
// from menu images.
func TestReorderMediaArtifactsChangesOrderNotCaptureIndex(t *testing.T) {
	t.Parallel()

	privateMedia := &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}
	mediaBuilder := mediaArtifactBuilderFunc(func(
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
			CaptureFingerprint:      testFingerprint(t, "reordered-media"),
			RequirementsFingerprint: requirements,
			Artifacts: []api.MediaArtifact{
				{
					ID:       "screen-a",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Index:    0,
					Order:    0,
					Selected: true,
				},
				{
					ID:       "screen-b",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Index:    1,
					Order:    1,
					Selected: true,
				},
				{
					ID:       "menu-a",
					Kind:     api.MediaArtifactDVDMenu,
					Purpose:  api.ScreenshotPurposeMenu,
					Index:    0,
					Order:    2,
					Selected: true,
				},
			},
			Status: api.StageStatusCompleted,
		}, privateMedia, nil
	})
	module, _ := newTestModule(
		t,
		testPreparer(),
		WithTrackerPreflightBuilder(readyPreflightBuilder(t)),
		WithDupeAssessmentBuilder(readyDupeBuilder(t)),
		WithMediaArtifactBuilder(mediaBuilder),
		WithDescriptionBuilder(&descriptionBuilderFake{testing: t}),
		WithUploadPlanBuilder(&uploadPlanBuilderFake{testing: t}),
	)
	result := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-media-reorder"})
	result = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	result = executeTestPublication(t, module, trackerContextPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Catalog:          testCatalog(t),
		Runtime:          testRuntime(t),
		Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	})
	projectionSet := testProjectionSet(t)
	for index := range projectionSet.Projections {
		projectionSet.Projections[index].Artifacts.ScreenshotCount = 2
		projectionSet.Projections[index].Artifacts.DVDMenuCount = 1
	}
	result = executeTestPublication(t, module, projectionSetPublication{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Snapshot:         projectionSet,
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
		Instructions:     api.MediaCaptureInstructions{ScreenshotCount: 2, Purpose: api.ScreenshotPurposeFinal},
	})

	reordered := executeCommand(t, module, ReorderMediaArtifactsCommand{
		WorkflowID:       result.Workflow.ID,
		ExpectedRevision: result.Workflow.Revision,
		Media:            *result.Workflow.Media,
		ArtifactIDs:      []api.PublicResourceID{"menu-a", "screen-b", "screen-a"},
		IdempotencyKey:   "reorder-media",
	})
	if reordered.Media == nil || len(reordered.Media.Artifacts) != 3 {
		t.Fatalf("reordered media = %#v", reordered.Media)
	}
	wantOrder := map[api.PublicResourceID]int{
		"menu-a":   0,
		"screen-b": 1,
		"screen-a": 2,
	}
	wantIndex := map[api.PublicResourceID]int{
		"screen-a": 0,
		"screen-b": 1,
		"menu-a":   0,
	}
	wantKind := map[api.PublicResourceID]api.MediaArtifactKind{
		"screen-a": api.MediaArtifactScreenshot,
		"screen-b": api.MediaArtifactScreenshot,
		"menu-a":   api.MediaArtifactDVDMenu,
	}
	for _, artifact := range reordered.Media.Artifacts {
		if artifact.Order != wantOrder[artifact.ID] {
			t.Fatalf("artifact %q order = %d, want %d", artifact.ID, artifact.Order, wantOrder[artifact.ID])
		}
		if artifact.Index != wantIndex[artifact.ID] {
			t.Fatalf("reorder moved the capture slot: artifact %q index = %d, want %d", artifact.ID, artifact.Index, wantIndex[artifact.ID])
		}
		if artifact.Kind != wantKind[artifact.ID] {
			t.Fatalf("artifact %q kind = %q, want %q", artifact.ID, artifact.Kind, wantKind[artifact.ID])
		}
	}
	if privateMedia.stats.staged != 0 || privateMedia.stats.deletions != 0 {
		t.Fatalf("reorder touched private media: %#v", privateMedia.stats)
	}
}

func TestRefreshMutatedMediaStatusPreservesOnlyGenuineMediaActions(t *testing.T) {
	t.Parallel()

	projections := []api.TrackerReleaseProjection{{
		TrackerID: "SYNTHETIC",
		Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1, DVDMenuCount: 1},
	}}
	snapshot := api.MediaArtifactSet{
		Status: api.StageStatusCompleted,
		Artifacts: []api.MediaArtifact{
			{
				ID:       "screen",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
			},
			{
				ID:       "hosted-screen",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
				Source:   "screen",
			},
		},
		RequiredActions: []api.RequiredAction{{
			Kind:   api.RequiredActionProvideTrackerInput,
			Prompt: "Stale media action.",
		}},
	}

	refreshMutatedMediaStatus(&snapshot, projections)
	if snapshot.Status != api.StageStatusBlocked || len(snapshot.RequiredActions) != 1 ||
		snapshot.RequiredActions[0].Kind != api.RequiredActionProvideTrackerInput {
		t.Fatalf("genuine missing menu action was not republished: %#v", snapshot)
	}

	snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
		ID:       "menu",
		Kind:     api.MediaArtifactDVDMenu,
		Purpose:  api.ScreenshotPurposeMenu,
		Selected: true,
	})
	refreshMutatedMediaStatus(&snapshot, projections)
	if snapshot.Status != api.StageStatusCompleted || len(snapshot.RequiredActions) != 0 {
		t.Fatalf("satisfied media retained stale action: %#v", snapshot)
	}
}

func waitForWorkflowOperation(
	t *testing.T,
	module *Module,
	workflowID api.WorkflowID,
	operationID api.WorkflowOperationID,
	ready func(api.WorkflowOperationStatus) bool,
) api.WorkflowOperationStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := module.Operation(context.Background(), testOwnerID, workflowID, operationID)
		if err != nil {
			t.Fatalf("poll operation: %v", err)
		}
		if ready(status) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not reach expected state")
	return api.WorkflowOperationStatus{}
}

func TestModuleCancelWorkflowClearsAuthorityAndIsIdempotent(t *testing.T) {
	t.Parallel()

	module, _ := newTestModule(t, testPreparer())
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "workflow-cancel-command"})
	prepared := executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: "C:\\releases\\Example.Release.2026.1080p-GRP"},
	})
	if err := module.private.Put(
		testOwnerID,
		prepared.Workflow.ID,
		"retained-authority",
		"private",
		time.Date(2026, time.July, 20, 13, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("retain private authority: %v", err)
	}
	command := CancelWorkflowCommand{
		WorkflowID:       prepared.Workflow.ID,
		ExpectedRevision: prepared.Workflow.Revision,
		Reason:           "operator request",
		IdempotencyKey:   "cancel-once",
	}
	canceled := executeCommand(t, module, command)
	if canceled.Workflow.Status != api.WorkflowStatusCanceled || canceled.Workflow.Release != nil {
		t.Fatalf("canceled workflow = %#v", canceled.Workflow)
	}
	if _, err := module.private.Get(
		testOwnerID,
		prepared.Workflow.ID,
		"retained-authority",
		time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC),
	); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("canceled private authority error = %v, want %v", err, ErrPrivateResourceUnavailable)
	}
	retried := executeCommand(t, module, command)
	if retried.Workflow.Revision != canceled.Workflow.Revision || retried.Workflow.Status != api.WorkflowStatusCanceled {
		t.Fatalf("cancel retry = %#v, want revision %d", retried.Workflow, canceled.Workflow.Revision)
	}
	_, err := module.Execute(context.Background(), testOwnerID, CancelWorkflowCommand{
		WorkflowID:       canceled.Workflow.ID,
		ExpectedRevision: canceled.Workflow.Revision,
		Reason:           "second cancellation",
		IdempotencyKey:   "cancel-twice",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second cancellation error = %v, want %v", err, ErrInvalidTransition)
	}
}

func TestMemoryPrivateResourceStoreIsolationExpirySingleUseAndRestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	store := NewMemoryPrivateResourceStore()
	if err := store.Put(testOwnerID, "workflow-private", "plan-1", "authority", now.Add(time.Minute)); err != nil {
		t.Fatalf("put private resource: %v", err)
	}
	if _, err := store.Get("different-owner", "workflow-private", "plan-1", now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("foreign owner error = %v, want %v", err, ErrPrivateResourceUnavailable)
	}
	value, err := store.Consume(testOwnerID, "workflow-private", "plan-1", now)
	if err != nil || value != "authority" {
		t.Fatalf("consume private resource = %#v, %v", value, err)
	}
	if _, err := store.Consume(testOwnerID, "workflow-private", "plan-1", now); !errors.Is(err, ErrPrivateResourceConsumed) {
		t.Fatalf("reused resource error = %v, want %v", err, ErrPrivateResourceConsumed)
	}
	if err := store.Put(testOwnerID, "workflow-private", "expired", "authority", now); err != nil {
		t.Fatalf("put expiring resource: %v", err)
	}
	if _, err := store.Get(testOwnerID, "workflow-private", "expired", now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("expired resource error = %v, want %v", err, ErrPrivateResourceUnavailable)
	}
	if err := store.Put(testOwnerID, "workflow-private", "restart", "authority", now.Add(time.Minute)); err != nil {
		t.Fatalf("put restart resource: %v", err)
	}
	store.InvalidateAll()
	if _, err := store.Get(testOwnerID, "workflow-private", "restart", now); !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("restart resource error = %v, want %v", err, ErrPrivateResourceUnavailable)
	}
}

func TestResolveReconciliationRequiresExactAnswerAndForcesFreshReview(t *testing.T) {
	t.Parallel()

	module, repository := newTestModule(t, testPreparer())
	ctx := context.Background()
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-reconcile")
	operationID := api.WorkflowOperationID("operation-reconcile")
	started := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          workflowID,
		OperationID:         operationID,
		EffectID:            "effect-reconcile",
		Kind:                string(api.WorkflowExternalEffectTrackerSubmission),
		ScopeID:             "ALPHA",
		SemanticFingerprint: "semantic-reconcile",
		Status:              api.WorkflowEffectStatusStarted,
		StartedAt:           now,
		UpdatedAt:           now,
	}
	if _, _, err := repository.BeginEffect(ctx, started); err != nil {
		t.Fatalf("begin effect: %v", err)
	}
	if err := repository.MarkOperationEffectsUnknown(ctx, testOwnerID, workflowID, operationID, now.Add(time.Second)); err != nil {
		t.Fatalf("mark effect unknown: %v", err)
	}
	action := api.RequiredAction{
		ID:               "action-reconcile",
		Kind:             api.RequiredActionReconcileSubmission,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: 2,
		TrackerID:        "ALPHA",
		EffectKind:       api.WorkflowExternalEffectTrackerSubmission,
		EffectScopeID:    "ALPHA",
		Prompt:           "Verify the external effect.",
		Options: []api.RequiredActionOption{{
			Value: api.RequiredActionReconcileNotCompleted,
			Label: "Confirmed not completed",
		}},
		CreatedAt: now,
	}
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:              workflowID,
			Revision:        2,
			Status:          api.WorkflowStatusBlocked,
			Dupes:           &api.DupeAssessmentRef{ID: "dupes-1", Revision: 1},
			Media:           &api.MediaArtifactSetRef{ID: "media-1", Revision: 1},
			Descriptions:    &api.DescriptionSetRef{ID: "descriptions-1", Revision: 1},
			DryRun:          &api.UploadDryRunResultRef{ID: "dry-run-1", Revision: 1},
			UploadResult:    &api.UploadResultRef{ID: "upload-result-1", Revision: 2},
			RequiredActions: []api.RequiredAction{action},
			Failures: []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureUnknownOutcome,
					Operation: api.OperationKindUploadExecute,
					Message:   "Unknown result.",
					Recovery:  api.OperationRecoveryConfirm,
				},
				TrackerID: "ALPHA",
			}},
		},
	}
	invalid := ResolveActionCommand{
		WorkflowID:       workflowID,
		ExpectedRevision: 2,
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: 2,
			SelectedValues:   []string{"completed"},
		},
	}
	if _, err := module.resolveAction(ctx, testOwnerID, &state, 3, now.Add(2*time.Second), invalid); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid reconciliation error=%v", err)
	}
	probe := started
	probe.EffectID = "effect-probe"
	probe.StartedAt = now.Add(3 * time.Second)
	probe.UpdatedAt = probe.StartedAt
	if _, _, err := repository.BeginEffect(ctx, probe); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		t.Fatalf("effect was unfenced by invalid answer: %v", err)
	}

	valid := invalid
	valid.Answer.SelectedValues = []string{api.RequiredActionReconcileNotCompleted}
	if _, err := module.resolveAction(ctx, testOwnerID, &state, 3, now.Add(4*time.Second), valid); err != nil {
		t.Fatalf("resolve reconciliation: %v", err)
	}
	if state.Workflow.Dupes != nil || state.Workflow.Media != nil || state.Workflow.Descriptions != nil ||
		state.Workflow.DryRun != nil || state.Workflow.UploadResult != nil || len(state.Workflow.RequiredActions) != 0 ||
		len(state.Workflow.Failures) != 0 || state.Workflow.Status != api.WorkflowStatusActive {
		t.Fatalf("reconciled workflow=%#v", state.Workflow)
	}
	retry := started
	retry.EffectID = "effect-retry"
	retry.StartedAt = now.Add(5 * time.Second)
	retry.UpdatedAt = retry.StartedAt
	if _, idempotent, err := repository.BeginEffect(ctx, retry); err != nil || idempotent {
		t.Fatalf("begin reconciled retry idempotent=%v err=%v", idempotent, err)
	}
}

func TestResolveImageHostingReconciliationInvalidatesMissingPrivateMedia(t *testing.T) {
	t.Parallel()

	module, repository := newTestModule(t, testPreparer())
	ctx := context.Background()
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	workflowID := api.WorkflowID("workflow-image-reconcile")
	operationID := api.WorkflowOperationID("operation-image-reconcile")
	effectScopeID := "imgbox:media-1"
	started := api.ReleaseWorkflowEffectRecord{
		OwnerID:             testOwnerID,
		WorkflowID:          workflowID,
		OperationID:         operationID,
		EffectID:            "effect-image-reconcile",
		Kind:                string(api.WorkflowExternalEffectImageHosting),
		ScopeID:             effectScopeID,
		SemanticFingerprint: "semantic-image-reconcile",
		Status:              api.WorkflowEffectStatusStarted,
		StartedAt:           now,
		UpdatedAt:           now,
	}
	if _, _, err := repository.BeginEffect(ctx, started); err != nil {
		t.Fatalf("begin image-host effect: %v", err)
	}
	if err := repository.MarkOperationEffectsUnknown(ctx, testOwnerID, workflowID, operationID, now.Add(time.Second)); err != nil {
		t.Fatalf("mark image-host effect unknown: %v", err)
	}
	action := api.RequiredAction{
		ID:               "action-image-reconcile",
		Kind:             api.RequiredActionReconcileSubmission,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: 2,
		EffectKind:       api.WorkflowExternalEffectImageHosting,
		EffectScopeID:    effectScopeID,
		Prompt:           "Verify the image-host effect.",
		Options: []api.RequiredActionOption{{
			Value: api.RequiredActionReconcileNotCompleted,
			Label: "Confirmed not completed",
		}},
		CreatedAt: now,
	}
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:              workflowID,
			Revision:        2,
			Status:          api.WorkflowStatusBlocked,
			Dupes:           &api.DupeAssessmentRef{ID: "dupes-1", Revision: 1},
			Media:           &api.MediaArtifactSetRef{ID: "media-1", Revision: 1},
			Descriptions:    &api.DescriptionSetRef{ID: "descriptions-1", Revision: 1},
			DryRun:          &api.UploadDryRunResultRef{ID: "dry-run-1", Revision: 1},
			RequiredActions: []api.RequiredAction{action},
			Failures: []api.WorkflowFailure{{
				Failure: api.OperationFailure{
					Code:      api.OperationFailureUnknownOutcome,
					Operation: api.OperationKindImageHosting,
					Message:   "Unknown image-host result.",
					Recovery:  api.OperationRecoveryConfirm,
				},
				Resource: effectScopeID,
			}},
		},
		Media: map[api.MediaArtifactSetID]api.MediaArtifactSet{
			"media-1": {
				ID:              "media-1",
				Revision:        1,
				RequiredActions: []api.RequiredAction{action},
			},
		},
	}
	_, err := module.resolveAction(ctx, testOwnerID, &state, 3, now.Add(2*time.Second), ResolveActionCommand{
		WorkflowID:       workflowID,
		ExpectedRevision: 2,
		Answer: api.RequiredActionAnswer{
			ActionID:         action.ID,
			WorkflowRevision: 2,
			SelectedValues:   []string{api.RequiredActionReconcileNotCompleted},
		},
	})
	if err != nil {
		t.Fatalf("resolve missing private image-host reconciliation: %v", err)
	}
	if state.Workflow.Dupes == nil || state.Workflow.Media != nil || state.Workflow.Descriptions != nil ||
		state.Workflow.DryRun != nil || len(state.Workflow.RequiredActions) != 0 || len(state.Workflow.Failures) != 0 ||
		state.Workflow.Status != api.WorkflowStatusActive {
		t.Fatalf("reconciled image-host workflow=%#v", state.Workflow)
	}
	retry := started
	retry.EffectID = "effect-image-retry"
	retry.StartedAt = now.Add(3 * time.Second)
	retry.UpdatedAt = retry.StartedAt
	if _, idempotent, err := repository.BeginEffect(ctx, retry); err != nil || idempotent {
		t.Fatalf("begin reconciled image-host retry idempotent=%v err=%v", idempotent, err)
	}
}

func TestStampMediaActionsPublishesPendingActionIdentity(t *testing.T) {
	t.Parallel()

	module, _ := newTestModule(t, testPreparer())
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	snapshot := api.MediaArtifactSet{RequiredActions: []api.RequiredAction{{
		Kind:   api.RequiredActionProvideTrackerInput,
		Prompt: "Choose screenshot frames, then retry media capture.",
	}}}
	if err := module.stampMediaActions(&snapshot, 7, now); err != nil {
		t.Fatalf("stamp media actions: %v", err)
	}
	action := snapshot.RequiredActions[0]
	if action.ID == "" || action.Status != api.RequiredActionStatusPending || action.WorkflowRevision != 7 || action.CreatedAt != now {
		t.Fatalf("stamped media action = %#v", action)
	}
}

func newTestModule(t *testing.T, preparer ReleasePreparer, options ...Option) (*Module, *MemoryRepository) {
	t.Helper()
	repository := NewMemoryRepository()
	privateStore := NewMemoryPrivateResourceStore()
	moduleOptions := make([]Option, 0, 2+len(options))
	moduleOptions = append(moduleOptions,
		WithClock(fixedClock{now: time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)}),
		WithIDGenerator(&sequenceIDGenerator{}),
	)
	moduleOptions = append(moduleOptions, options...)
	module, err := New(
		repository,
		privateStore,
		preparer,
		moduleOptions...,
	)
	if err != nil {
		t.Fatalf("new workflow module: %v", err)
	}
	return module, repository
}

func testPreparer() ReleasePreparer {
	return ReleasePreparerFunc{
		PrepareFunc: func(_ context.Context, input api.PrepareInput) (api.PrepareResult, error) {
			return api.PrepareResult{Release: api.PreparedRelease{
				Generation: 1,
				Source: api.SourceManifest{
					SourcePath: input.SourcePath,
				},
				Naming: api.NamingFacts{ReleaseName: "Example.Release.2026.1080p-GRP"},
			}}, nil
		},
		DisplayFunc: func(_ context.Context, _ api.ReleaseRef) (api.PreparedReleaseDisplay, error) {
			return api.PreparedReleaseDisplay{ReleaseName: "Example.Release.2026.1080p-GRP"}, nil
		},
		SubjectFunc: func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
			return api.UploadSubject{
				SourcePath:  input.Release.SourcePath,
				Trackers:    append([]string(nil), input.Trackers...),
				ReleaseName: "Example.Release.2026.1080p-GRP",
			}, nil
		},
		DuplicateFunc: func(_ context.Context, input api.DuplicateCheckInput) (api.DuplicateSubject, error) {
			return api.DuplicateSubject{
				SourcePath:  input.Release.SourcePath,
				ReleaseName: "Example.Release.2026.1080p-GRP",
			}, nil
		},
	}
}

func executeCommand(t *testing.T, module *Module, command mutation) CommandResult {
	t.Helper()
	result, err := module.execute(context.Background(), testOwnerID, command)
	if err != nil {
		t.Fatalf("execute %T: %v", command, err)
	}
	return result
}

func executeTestPublication(t *testing.T, module *Module, publication any) CommandResult {
	t.Helper()
	result, err := executeTestPublicationRaw(module, publication)
	if err != nil {
		t.Fatalf("publish %T: %v", publication, err)
	}
	return result
}

// executeTestPublicationRaw installs synthetic immutable stage outputs for
// transition tests without restoring publication commands to the runtime API.
func executeTestPublicationRaw(module *Module, publication any) (CommandResult, error) {
	var workflowID api.WorkflowID
	var expectedRevision api.WorkflowRevision
	switch typed := publication.(type) {
	case trackerContextPublication:
		workflowID, expectedRevision = typed.WorkflowID, typed.ExpectedRevision
	case projectionSetPublication:
		workflowID, expectedRevision = typed.WorkflowID, typed.ExpectedRevision
	case dupeAssessmentPublication:
		workflowID, expectedRevision = typed.WorkflowID, typed.ExpectedRevision
	default:
		return CommandResult{}, fmt.Errorf("unsupported test publication %T", publication)
	}
	ctx := context.Background()
	state, err := module.repository.Load(ctx, testOwnerID, workflowID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("load test workflow publication: %w", err)
	}
	if state.Workflow.Revision != expectedRevision {
		return CommandResult{}, ErrRevisionConflict
	}
	nextRevision := expectedRevision + 1
	now := module.clock.Now().UTC()
	var result CommandResult
	switch typed := publication.(type) {
	case trackerContextPublication:
		result, err = module.setTrackerContext(ctx, testOwnerID, &state, nextRevision, now, typed)
	case projectionSetPublication:
		result, err = module.publishProjections(ctx, testOwnerID, &state, nextRevision, now, typed)
	case dupeAssessmentPublication:
		result, err = module.publishDupes(&state, nextRevision, now, typed)
	}
	if err != nil {
		return CommandResult{}, err
	}
	state.Workflow.Revision = nextRevision
	state.Workflow.UpdatedAt = now
	result.Workflow = state.Workflow
	if err := state.Workflow.Validate(); err != nil {
		return CommandResult{}, fmt.Errorf("validate test workflow publication: %w", err)
	}
	if err := module.repository.Save(ctx, testOwnerID, expectedRevision, state); err != nil {
		return CommandResult{}, fmt.Errorf("save test workflow publication: %w", err)
	}
	return cloneCommandResult(result)
}

func testCatalog(t *testing.T) api.TrackerCatalogSnapshot {
	t.Helper()
	return api.TrackerCatalogSnapshot{
		CatalogVersion: "test-v1",
		Trackers: []api.TrackerCatalogDescriptor{
			{
				TrackerID:         "ALPHA",
				DisplayName:       "Alpha",
				ProjectorVersion:  "v1",
				PolicyFingerprint: testFingerprint(t, "alpha-policy"),
			},
			{
				TrackerID:         "BETA",
				DisplayName:       "Beta",
				ProjectorVersion:  "v1",
				PolicyFingerprint: testFingerprint(t, "beta-policy"),
			},
		},
	}
}

func testRuntime(t *testing.T) api.TrackerRuntimeSnapshot {
	t.Helper()
	return api.TrackerRuntimeSnapshot{
		RuntimeGeneration: "runtime-v1",
		Trackers: []api.TrackerRuntimeEntry{
			{
				TrackerID:         "ALPHA",
				Configured:        true,
				ConfigFingerprint: testFingerprint(t, "alpha-config"),
			},
			{
				TrackerID:         "BETA",
				Configured:        true,
				ConfigFingerprint: testFingerprint(t, "beta-config"),
			},
		},
	}
}

func testProjectionSet(t *testing.T) api.TrackerReleaseProjectionSet {
	t.Helper()
	return api.TrackerReleaseProjectionSet{
		InputFingerprint:  testFingerprint(t, "projection-input"),
		PolicyFingerprint: testFingerprint(t, "projection-policy"),
		Projections: []api.TrackerReleaseProjection{
			testProjection(t, "ALPHA", "Example.Release.2026.ALPHA-GRP"),
			testProjection(t, "BETA", "Example.Release.2026.BETA-GRP"),
		},
		Status: api.StageStatusReady,
	}
}

func readyPreflightBuilder(t *testing.T) TrackerPreflightBuilder {
	t.Helper()
	return trackerPreflightBuilderFunc(func(
		_ context.Context,
		_ api.UploadSubject,
		_ api.TrackerCatalogSnapshot,
		_ api.TrackerRuntimeSnapshot,
		initial api.TrackerReleaseProjectionSet,
		now time.Time,
	) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		results := make([]api.TrackerPreflightResult, 0, len(initial.Projections))
		finalized := append([]api.TrackerReleaseProjection(nil), initial.Projections...)
		for _, projection := range initial.Projections {
			fingerprint, err := api.CanonicalWorkflowFingerprint(projection)
			if err != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("fingerprint ready preflight projection: %w", err)
			}
			results = append(results, api.TrackerPreflightResult{
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
			})
		}
		return api.TrackerPreflightAssessment{
			InputFingerprint: testFingerprint(t, "ready-preflight"),
			Results:          results,
			ExpiresAt:        now.Add(time.Hour),
		}, finalized, nil
	})
}

func testProjection(t *testing.T, trackerID api.TrackerID, name string) api.TrackerReleaseProjection {
	t.Helper()
	criteria := api.TrackerDuplicateCriteria{Name: name}
	criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(criteria)
	if err != nil {
		t.Fatalf("projection criteria fingerprint: %v", err)
	}
	return api.TrackerReleaseProjection{
		TrackerID:            trackerID,
		DisplayName:          string(trackerID),
		CanonicalReleaseName: "Example.Release.2026.1080p-GRP",
		UploadReleaseName:    name,
		DuplicateCriteria:    criteria,
		Artifacts:            api.TrackerArtifactRequirements{Description: true},
		InputFingerprint:     testFingerprint(t, string(trackerID)+"-input"),
		CatalogFingerprint:   testFingerprint(t, string(trackerID)+"-catalog"),
		ConfigFingerprint:    testFingerprint(t, string(trackerID)+"-config"),
		ProjectorFingerprint: testFingerprint(t, string(trackerID)+"-projector"),
		CriteriaFingerprint:  criteriaFingerprint,
		Readiness:            api.ReadinessStatusReady,
		DupeReady:            true,
		UploadReady:          true,
	}
}

func testDupes(t *testing.T) api.DupeAssessment {
	t.Helper()
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	return api.DupeAssessment{
		InputFingerprint: testFingerprint(t, "dupe-input"),
		Results: []api.TrackerDupeAssessment{
			testDupeResult(t, "ALPHA", "Example.Release.2026.ALPHA-GRP", now),
			testDupeResult(t, "BETA", "Example.Release.2026.BETA-GRP", now),
		},
		Status:    api.StageStatusCompleted,
		ExpiresAt: now.Add(time.Hour),
	}
}

func testDupeResult(t *testing.T, trackerID api.TrackerID, name string, now time.Time) api.TrackerDupeAssessment {
	t.Helper()
	return api.TrackerDupeAssessment{
		TrackerID:             trackerID,
		UploadReleaseName:     name,
		ProjectionFingerprint: testFingerprint(t, string(trackerID)+"-projection"),
		CriteriaFingerprint:   testFingerprint(t, string(trackerID)+"-criteria"),
		Criteria:              api.TrackerDuplicateCriteria{Name: name},
		Decision:              api.DupeDecisionNoMatch,
		Status:                api.StageStatusCompleted,
		CheckedAt:             now,
		FreshUntil:            now.Add(time.Hour),
	}
}

func testFingerprint(t *testing.T, value string) api.WorkflowFingerprint {
	t.Helper()
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint %q: %v", value, err)
	}
	return fingerprint
}
