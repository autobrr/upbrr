// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type projectionDiscoveryReadiness struct{ calls int }

func (e *projectionDiscoveryReadiness) Requirements(context.Context, []api.TrackerID) (api.MetadataRequirementSet, error) {
	e.calls++
	return api.MetadataRequirementSet{}, errors.New("unexpected metadata demand refresh")
}
func (e *projectionDiscoveryReadiness) Evaluate(context.Context, api.UploadSubject, []api.TrackerID) (api.InputReadinessEvaluation, error) {
	e.calls++
	return api.InputReadinessEvaluation{}, errors.New("unexpected canonical readiness evaluation")
}

func TestProjectionContinuationNeverPreparesOrAssesses(t *testing.T) {
	for _, scenario := range []string{"unprepared", "changed input", "changed corrections", "stale generation", "current generation"} {
		t.Run(scenario, func(t *testing.T) {
			preparer := testPreparer()
			base := preparer.PrepareFunc
			prepareCalls, preflightCalls := 0, 0
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				prepareCalls++
				return base(ctx, input)
			}
			readiness := &projectionDiscoveryReadiness{}
			preflight := trackerPreflightBuilderFunc(func(context.Context, api.UploadSubject, api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerReleaseProjectionSet, time.Time) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
				preflightCalls++
				return api.TrackerPreflightAssessment{}, nil, errors.New("unexpected auth, bans, or claims preflight")
			})
			module, _ := newTestModule(t, preparer, WithInputReadinessEvaluator(readiness), WithTrackerProjectionBuilder(trackerProjectionBuilderFunc(func(context.Context, api.ReleaseSnapshot, api.UploadSubject, []api.TrackerID, map[api.TrackerID]api.TrackerProjectionInstructions, map[api.TrackerID]api.WorkflowFingerprint, api.WorkflowExecutionMode) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
				return testCatalog(t), testRuntime(t), api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}}, testProjectionSet(t), nil
			})), WithTrackerPreflightBuilder(preflight))
			current := executeCommand(t, module, CreateWorkflowCommand{})
			input := api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")}
			if scenario != "unprepared" {
				current = executeCommand(t, module, PrepareReleaseCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					Input:            input,
				})
			}
			request := api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
				IdempotencyKey: "discover",
				Goal:           api.WorkflowGoalTrackersProjected,
				Intent:         api.WorkflowIntent{Preparation: &input, TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
			}
			switch scenario {
			case "changed input":
				request.Intent.Preparation = &api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Another.Release.mkv"), Force: true}
			case "changed corrections":
				preparer.CorrectionsCurrentFunc = func(context.Context, string, uint64) (bool, error) { return false, nil }
				module.preparer = preparer
			case "stale generation":
				preparer.SubjectFunc = func(context.Context, api.UploadSubjectInput) (api.UploadSubject, error) {
					return api.UploadSubject{}, &preparedrelease.StalePreparationError{Generation: 1}
				}
				module.preparer = preparer
			}
			updated, err := module.Continue(t.Context(), testOwnerID, request)
			if scenario == "unprepared" || scenario == "changed input" || scenario == "changed corrections" {
				failure, ok := api.AsOperationFailure(err)
				if !ok || failure.Code != api.OperationFailureMissingPrerequisite {
					t.Fatalf("expected preparation required, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if updated.Operation == nil {
					t.Fatal("no projection operation")
				}
				status := waitForWorkflowOperation(t, module, current.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
				if scenario == "stale generation" {
					if status.Status != api.StageStatusFailed {
						t.Fatalf("stale exact generation was accepted: %+v", status)
					}
				} else {
					if status.Status != api.StageStatusCompleted {
						t.Fatalf("projection failed: %+v", status)
					}
					current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
					if err != nil {
						t.Fatal(err)
					}
					request.Authority.ExpectedRevision = current.Workflow.Revision
					updated, err = module.Continue(t.Context(), testOwnerID, request)
					if err != nil || updated.Workflow.Revision != current.Workflow.Revision {
						t.Fatalf("settled projection changed: %v", err)
					}
					// A matching retained projection must still reject an unavailable exact generation.
					preparer.SubjectFunc = func(context.Context, api.UploadSubjectInput) (api.UploadSubject, error) {
						return api.UploadSubject{}, &preparedrelease.StalePreparationError{Generation: 1}
					}
					module.preparer = preparer
					_, err = module.Continue(t.Context(), testOwnerID, request)
					if _, ok := errors.AsType[*preparedrelease.StalePreparationError](err); !ok {
						t.Fatalf("retained projection bypassed exact generation: %v", err)
					}
				}
			}
			wantPrepare := 1
			if scenario == "unprepared" {
				wantPrepare = 0
			}
			if prepareCalls != wantPrepare || preflightCalls != 0 || readiness.calls != 0 {
				t.Fatalf("discovery did remote/preparation work: prepare=%d preflight=%d readiness=%d", prepareCalls, preflightCalls, readiness.calls)
			}
		})
	}
}

type legacyGroupQuestionnaireDefinition struct {
	trackers.Definition
	answers trackers.TrackerAnswerSchemaProvider
}

func (d legacyGroupQuestionnaireDefinition) ProjectionQuestionnaire(input trackers.PreparationInput) *api.TrackerQuestionnaire {
	return d.answers.TrackerAnswerSchema(input)
}

func TestProjectionContinuationRefreshesPersistedGroupSchemasWithoutRepreparing(t *testing.T) {
	for _, trackerID := range []api.TrackerID{"PTP", "GPW"} {
		t.Run(string(trackerID), func(t *testing.T) {
			currentRegistry := trackerimpl.MustNewRegistry()
			descriptor, ok := currentRegistry.LookupDescriptor(string(trackerID))
			if !ok {
				t.Fatal("tracker missing")
			}
			oldRegistry := trackers.NewRegistry()
			answerProvider, ok := descriptor.Definition.(trackers.TrackerAnswerSchemaProvider)
			if !ok {
				t.Fatal("private answer schema missing")
			}
			descriptor.Definition = legacyGroupQuestionnaireDefinition{Definition: descriptor.Definition, answers: answerProvider}
			descriptor.ProjectorVersion = "standalone-v2-questionnaire-v2"
			if err := oldRegistry.RegisterDescriptor(descriptor); err != nil {
				t.Fatal(err)
			}
			oldProjector, err := trackers.NewWorkflowProjector(oldRegistry, config.Config{}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			// Use a matching single-tracker catalog so the schema version is the sole catalog change.
			newRegistry := trackers.NewRegistry()
			descriptor, _ = currentRegistry.LookupDescriptor(string(trackerID))
			if err := newRegistry.RegisterDescriptor(descriptor); err != nil {
				t.Fatal(err)
			}
			newProjector, err := trackers.NewWorkflowProjector(newRegistry, config.Config{}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			database, err := db.Open(filepath.Join(t.TempDir(), "projection-reload.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if err := database.Migrate(); err != nil {
				t.Fatal(err)
			}
			repository, err := NewPersistentRepository(database)
			if err != nil {
				t.Fatal(err)
			}
			preparations := 0
			preparer := testPreparer()
			base := preparer.PrepareFunc
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				preparations++
				result, err := base(ctx, input)
				result.Release.Compatibility.ContractVersion = preparedrelease.ContractVersion
				return result, err
			}
			first, err := New(repository, NewMemoryPrivateResourceStore(), preparer, WithTrackerProjectionBuilder(oldProjector), WithProcessEpoch("old-schema"))
			if err != nil {
				t.Fatal(err)
			}
			current := executeCommand(t, first, CreateWorkflowCommand{})
			current = executeCommand(t, first, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.mkv")},
			})
			current = executeCommand(t, first, ProjectTrackersCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				TrackerIDs:       []api.TrackerID{trackerID},
			})
			oldProjection := *current.Workflow.TrackerProjections
			if len(current.Projections.Projections[0].Questionnaire) < 5 {
				t.Fatal("fixture lacks legacy group schema")
			}
			if err := first.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(repository, NewMemoryPrivateResourceStore(), preparer, WithTrackerProjectionBuilder(newProjector), WithProcessEpoch("current-schema"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Shutdown(context.Background()) })
			current, err = reopened.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			prepared := current.Release.Release
			updated, err := reopened.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
				Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
				Goal:           api.WorkflowGoalTrackersProjected,
				IdempotencyKey: "refresh-schema",
				Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{trackerID}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if updated.Operation == nil {
				t.Fatal("legacy schema was treated as already reached")
			}
			waitForWorkflowOperation(t, reopened, current.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
			updated, err = reopened.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.Projections == nil || updated.Projections.ID == oldProjection.ID {
				t.Fatal("legacy projection was not replaced")
			}
			for _, field := range updated.Projections.Projections[0].Questionnaire {
				if field.Key == "poster" || field.Key == "poster_url" || field.Key == "tags" {
					t.Fatalf("legacy group field retained: %s", field.Key)
				}
			}
			if preparations != 1 || updated.Release.Release.Generation != prepared.Generation || updated.Release.Release.Compatibility != prepared.Compatibility || prepared.Compatibility.ContractVersion != "prepared-release-v26" {
				t.Fatal("schema upgrade changed prepared generation")
			}
		})
	}
}

func TestProjectionContinuationANTMissingTagsPublishesBlockedQuestion(t *testing.T) {
	registry := trackerimpl.MustNewRegistry()
	projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	preparer := testPreparer()
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{
			SourcePath:        input.Release.SourcePath,
			MediaInfoTextPath: filepath.Join(filepath.Dir(input.Release.SourcePath), "mediainfo.txt"),
			ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
TMDBID: 123,
 Title: "Example Movie",
 Year: 2026,
}},
			ReleaseName:       "Example.Movie.2026.1080p.BluRay.x264-GRP",
			Source:            "BluRay",
			Type:              "ENCODE",
			Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
			Release: api.ReleaseInfo{
				Title:      "Example Movie",
				Year:       2026,
				Category:   "MOVIE",
				Resolution: "1080p",
			},
		}, nil
	}
	module, _ := newTestModule(t, preparer, WithTrackerProjectionBuilder(projector))
	current := executeCommand(t, module, CreateWorkflowCommand{})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.mkv")},
	})
	updated, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
		IdempotencyKey: "ant-discovery",
		Goal:           api.WorkflowGoalTrackersProjected,
		Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{"ANT"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Operation == nil {
		t.Fatal("no discovery operation")
	}
	status := waitForWorkflowOperation(t, module, current.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
	if status.Status != api.StageStatusBlocked {
		t.Fatalf("ANT pending-question operation status=%s", status.Status)
	}
	updated, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Projections == nil || updated.Preflight != nil || updated.Dupes != nil {
		t.Fatal("question discovery ran downstream stages")
	}
	projection := updated.Projections.Projections[0]
	if projection.Readiness != api.ReadinessStatusBlocked || !slices.ContainsFunc(projection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool {
		return field.Key == "tags" && field.Required && field.Value == ""
	}) {
		t.Fatalf("missing required Tags: %+v", projection)
	}
}

func TestProjectionContinuationCanCancelWithoutPublishing(t *testing.T) {
	entered := make(chan struct{})
	projector := trackerProjectionBuilderFunc(func(ctx context.Context, _ api.ReleaseSnapshot, _ api.UploadSubject, _ []api.TrackerID, _ map[api.TrackerID]api.TrackerProjectionInstructions, _ map[api.TrackerID]api.WorkflowFingerprint, _ api.WorkflowExecutionMode) (api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerSelection, api.TrackerReleaseProjectionSet, error) {
		close(entered)
		<-ctx.Done()
		return api.TrackerCatalogSnapshot{}, api.TrackerRuntimeSnapshot{}, api.TrackerSelection{}, api.TrackerReleaseProjectionSet{}, ctx.Err()
	})
	module, _ := newTestModule(t, testPreparer(), WithTrackerProjectionBuilder(projector))
	current := executeCommand(t, module, CreateWorkflowCommand{})
	current = executeCommand(t, module, PrepareReleaseCommand{
WorkflowID: current.Workflow.ID,
 ExpectedRevision: current.Workflow.Revision,
 Input: api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.mkv")},
})
	updated, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
Authority: &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
 IdempotencyKey: "cancel-projection",
 Goal: api.WorkflowGoalTrackersProjected,
 Intent: api.WorkflowIntent{TrackerIDs: []api.TrackerID{"ALPHA"}},
})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Operation == nil {
		t.Fatal("projection did not start")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("projector did not start")
	}
	if _, err := module.CancelOperation(t.Context(), testOwnerID, current.Workflow.ID, updated.Operation.ID); err != nil {
		t.Fatal(err)
	}
	status := waitForWorkflowOperation(t, module, current.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
	if status.Status != api.StageStatusCanceled {
		t.Fatalf("canceled projection status=%s", status.Status)
	}
	updated, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Projections != nil || updated.Preflight != nil || updated.Dupes != nil || updated.Release.Release.Generation != current.Release.Release.Generation {
		t.Fatal("canceled discovery published downstream authority")
	}
}
