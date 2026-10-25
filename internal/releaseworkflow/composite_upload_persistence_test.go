// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeUploadHydrationRestoresDurableDemandForPreparedRelease(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	if err := os.WriteFile(sourcePath, []byte("synthetic media"), 0o600); err != nil {
		t.Fatalf("write preparation source: %v", err)
	}
	databasePath := filepath.Join(t.TempDir(), "composite-hydration.sqlite")
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
	preparerA := newCompositePersistencePreparer(t, repoA)
	requirements := api.MetadataRequirementSet{
		Version: "durable-composite-hydration-v1",
		Requirements: []api.MetadataRequirement{{
			Scope:       api.MetadataRequirementScopeAny,
			AnyOf:       []api.MetadataRequirementField{"original_title"},
			Disposition: api.RuleDispositionStrict,
		}},
	}
	moduleA, err := New(
		persistentA,
		NewMemoryPrivateResourceStore(),
		preparerA,
		WithClock(fixedClock{now: time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)}),
		WithIDGenerator(&sequenceIDGenerator{}),
		WithInputReadinessEvaluator(compositeUploadDemandEvaluator{requirements: requirements}),
	)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("new first workflow module: %v", err)
	}
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "durable-composite-hydration")
	request.Source.Path = sourcePath
	request.Duplicates.AllowUpload = []api.TrackerID{"beta"}
	session, instructions, err := normalizeCompositeUploadRequest(request)
	if err != nil {
		_ = repoA.Close()
		t.Fatalf("normalize composite request: %v", err)
	}
	created := executeCommand(t, moduleA, CreateWorkflowCommand{
		WorkflowID:          "durable-composite-hydration",
		Instructions:        instructions,
		IdempotencyKey:      request.IdempotencyKey,
		RequestFingerprint:  session.RequestFingerprint,
		TrackerDecisionMode: TrackerDecisionModePostDupeGate,
		Composite:           session,
	})
	prepared := executeCommand(t, moduleA, PrepareReleaseCommand{
		WorkflowID:       created.Workflow.ID,
		ExpectedRevision: created.Workflow.Revision,
		Input:            *session.Intent.Preparation,
		TrackerIDs:       session.Intent.TrackerIDs,
		IdempotencyKey:   "prepare-durable-composite-hydration",
	})
	if prepared.Release == nil {
		_ = repoA.Close()
		t.Fatal("prepared release is unavailable")
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
	preparerB := newCompositePersistencePreparer(t, repoB)
	moduleB, err := New(persistentB, NewMemoryPrivateResourceStore(), preparerB)
	if err != nil {
		t.Fatalf("new restarted workflow module: %v", err)
	}
	state, err := persistentB.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatalf("load restarted durable state: %v", err)
	}
	if state.Workflow.Release == nil {
		t.Fatal("restarted workflow release is unavailable")
	}
	release, ok := state.Releases[state.Workflow.Release.ID]
	if !ok {
		t.Fatalf("restarted release snapshot %q is unavailable", state.Workflow.Release.ID)
	}
	current := CommandResult{Release: &release}
	if state.Composite == nil || state.Composite.Intent.Preparation == nil ||
		!reflect.DeepEqual(state.PreparationDemand, requirements) || !reflect.DeepEqual(state.Composite.Intent.Preparation.MetadataRequirements, api.MetadataRequirementSet{}) {
		t.Fatalf("restarted composite preparation state = %#v", state)
	}
	if err := moduleB.hydrateCompositePreparedRelease(t.Context(), testOwnerID, current, state.Composite, state.PreparationDemand); err != nil {
		t.Fatalf("hydrate compatible prepared release: %v", err)
	}

	if !reflect.DeepEqual(state.Composite.DuplicateAllowUpload, []api.TrackerID{"BETA"}) {
		t.Fatalf("restored duplicate policy = %#v", state.Composite.DuplicateAllowUpload)
	}

	forced := *state.Composite.Intent.Preparation
	forced.SourcePath = current.Release.Release.Source.SourcePath
	forced.MetadataRequirements = state.PreparationDemand
	forced.Force = true
	if _, err := preparerB.Prepare(t.Context(), forced); err != nil {
		t.Fatalf("replace prepared generation: %v", err)
	}
	if err := moduleB.hydrateCompositePreparedRelease(t.Context(), testOwnerID, current, state.Composite, state.PreparationDemand); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("hydrate changed generation error = %v", err)
	}
}

func newCompositePersistencePreparer(t *testing.T, store preparedrelease.Store) *preparedrelease.Module {
	t.Helper()
	preparer, err := preparedrelease.New(store, compositePersistenceIdentityResolver{}, compositePersistenceCollector{})
	if err != nil {
		t.Fatalf("new prepared release module: %v", err)
	}
	return preparer
}

type compositePersistenceIdentityResolver struct{}

func (compositePersistenceIdentityResolver) Resolve(
	_ context.Context,
	request externalidentity.Request,
) (externalidentity.Result, error) {
	now := time.Now().UTC()
	return externalidentity.Result{
		Identity: api.ExternalIdentity{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			TMDBID:     1234567,
			Category:   api.CanonicalCategoryMovie,
			Provenance: api.IdentityProvenanceSet{
				TMDB:     api.IdentityProvenanceProvider,
				Category: api.IdentityProvenanceProvider,
			},
			Conflict: api.IdentityConflictNone,
			Resolution: api.IdentityResolutionKey{
				SourceFingerprint: request.SourceFingerprint,
				IntentFingerprint: "durable-composite-hydration",
				ContractVersion:   externalidentity.ContractVersion,
			},
			ResolvedAt: now,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: request.SourcePath,
			Generation: request.Generation,
			UpdatedAt:  now,
		},
	}, nil
}

type compositePersistenceCollector struct{}

func (compositePersistenceCollector) Collect(
	_ context.Context,
	request preparationstate.Request,
) (preparedrelease.CollectedFacts, error) {
	return preparedrelease.CollectedFacts{
		Naming: api.NamingFacts{
			Filename:         filepath.Base(request.Manifest.SourcePath),
			ReleaseName:      "Example.Release.2026.1080p-GRP",
			NamePresentation: api.ReleaseNamePresentation{Version: api.ReleaseNamePresentationVersionV1, OmitYear: true},
		},
		Assessments: api.ReleaseAssessments{
			MediaInfoUniqueID:       api.UniqueIDStatusPresent,
			MediaInfoEncodeSettings: api.EncodeSettingsStatusMissing,
			Naming:                  api.NamingAssessment{Status: api.NamingStatusComplete},
		},
	}, nil
}

func (compositePersistenceCollector) HydratePrivateResources(
	_ context.Context,
	request preparationstate.Request,
) (preparedrelease.CollectedResources, error) {
	return preparedrelease.CollectedResources{SourcePath: request.Manifest.SourcePath}, nil
}

var _ preparedrelease.Collector = compositePersistenceCollector{}
var _ preparedrelease.IdentityResolver = compositePersistenceIdentityResolver{}

// TestCompositeInputFailureSurvivesRestart exercises the real SQLite boundary:
// terminal failures survive a new coordinator, while correction uses fresh authority.
func TestCompositeInputFailureSurvivesRestart(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "composite-input-stop.sqlite")
	first, _, database, firstUploads := newCompositeSQLiteTestModule(t, databasePath)
	preparer := testPreparer()
	prepare := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		result, err := prepare(ctx, input)
		result.Release.Media.Source = ""
		result.Release.Media.Type = ""
		return result, err
	}
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{
			SourcePath:  input.Release.SourcePath,
			ReleaseName: "Example.Release.2026.1080p-GRP",
			Trackers:    append([]string(nil), input.Trackers...),
		}, nil
	}
	first.preparer = preparer
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "durable-input-stop")
	started, err := first.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	stopped := waitCompositeUploadTestOperation(t, first, started)
	if stopped.Operation.Status != api.StageStatusFailed || len(stopped.Operation.Failures) != 1 || firstUploads.execution != nil {
		t.Fatalf("input stop = %#v, execution = %#v", stopped.Operation, firstUploads.execution)
	}
	failure := stopped.Operation.Failures[0].Failure
	if failure.Code != api.OperationFailureInvalidInput || failure.Recovery != api.OperationRecoveryEditInput {
		t.Fatalf("input failure = %#v", failure)
	}
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, persistent, _, uploads := newCompositeSQLiteTestModule(t, databasePath)
	current, err := restarted.Current(t.Context(), testOwnerID, stopped.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := restarted.Operation(t.Context(), testOwnerID, stopped.Workflow.ID, stopped.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := persistent.Load(t.Context(), testOwnerID, stopped.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Workflow.Status != api.WorkflowStatusFailed || len(current.Workflow.Failures) != 1 ||
		current.Workflow.Failures[0].Failure != failure || operation.Status != api.StageStatusFailed ||
		len(operation.Failures) != 1 || operation.Failures[0].Failure != failure || operation.Result != nil ||
		state.Composite == nil || state.Composite.TerminalReason != "input-not-ready" || state.Composite.ActiveOperationID != "" {
		t.Fatalf("restarted workflow=%#v operation=%#v composite=%#v", current.Workflow, operation, state.Composite)
	}
	if current.InputReadiness == nil || !inputReadinessBlocked(current.InputReadiness) {
		t.Fatalf("restarted input correction evidence = %#v", current.InputReadiness)
	}
	if _, err := restarted.Operation(t.Context(), "other-owner", stopped.Workflow.ID, stopped.Operation.ID); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign operation read = %v", err)
	}
	current = executeCommand(t, restarted, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: request.Source.Path, Force: true},
	})
	request.Authority = &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision - 1}
	request.IdempotencyKey = "corrected-durable-input"
	if _, err := restarted.StartUpload(t.Context(), testOwnerID, request); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale correction = %v", err)
	}
	request.Authority.ExpectedRevision = current.Workflow.Revision
	if _, err := restarted.StartUpload(t.Context(), "other-owner", request); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign correction = %v", err)
	}
	resumed, err := restarted.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatalf("corrected restart: %v", err)
	}
	next := waitCompositeUploadTestOperation(t, restarted, resumed)
	if next.Workflow.ID != current.Workflow.ID || next.Operation.ID == operation.ID ||
		next.Operation.Status != api.StageStatusBlocked || next.Projections == nil ||
		inputReadinessBlocked(next.InputReadiness) || uploads.execution != nil {
		t.Fatalf("corrected workflow=%#v operation=%#v execution=%#v", next.Workflow, next.Operation, uploads.execution)
	}
	completed := approveCompositeUploadTrackers(t, restarted, next, []api.TrackerID{"ALPHA", "BETA"}, "approve-corrected-durable-input")
	if completed.Operation.Status != api.StageStatusExecuted || completed.Operation.Result == nil ||
		completed.Operation.Result.Kind != api.WorkflowOperationResultUpload || uploads.execution == nil || uploads.execution.executions != 1 {
		t.Fatalf("corrected upload operation=%#v execution=%#v", completed.Operation, uploads.execution)
	}
	replayed := approveCompositeUploadTrackers(t, restarted, next, []api.TrackerID{"ALPHA", "BETA"}, "approve-corrected-durable-input")
	if replayed.Operation.ID != completed.Operation.ID || uploads.execution.executions != 1 {
		t.Fatalf("approval replay repeated upload: operation=%#v execution=%#v", replayed.Operation, uploads.execution)
	}
	retained, err := restarted.Operation(t.Context(), testOwnerID, current.Workflow.ID, operation.ID)
	if err != nil || retained.Status != api.StageStatusFailed || len(retained.Failures) != 1 || retained.Failures[0].Failure != failure {
		t.Fatalf("original failure after retry = %#v, %v", retained, err)
	}
}

func newCompositeSQLiteTestModule(t *testing.T, databasePath string) (*Module, *PersistentRepository, *db.SQLiteRepository, *uploadPlanBuilderFake) {
	t.Helper()
	database, err := db.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	persistent, err := NewPersistentRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	module, _, uploads := newCompositeUploadTestModule(t)
	module.repository, module.operations, module.durability = persistent, persistent, persistent
	module.ids = randomIDGenerator{}
	t.Cleanup(func() { _ = module.Shutdown(context.Background()) })
	return module, persistent, database, uploads
}
