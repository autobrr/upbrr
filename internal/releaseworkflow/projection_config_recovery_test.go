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
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestProjectionContinuationRestoresConfigInvalidatedTrackerLane(t *testing.T) {
	registry := trackerimpl.MustNewRegistry()
	projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	preparer := testPreparer()
	prepare := preparer.PrepareFunc
	prepareCalls, preflightCalls := 0, 0
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		prepareCalls++
		return prepare(ctx, input)
	}
	readiness := &projectionDiscoveryReadiness{}
	preflight := trackerPreflightBuilderFunc(func(context.Context, api.UploadSubject, api.TrackerCatalogSnapshot, api.TrackerRuntimeSnapshot, api.TrackerReleaseProjectionSet, time.Time) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
		preflightCalls++
		return api.TrackerPreflightAssessment{}, nil, errors.New("unexpected remote preflight")
	})
	module, repository := newTestModule(t, preparer, WithTrackerProjectionBuilder(projector), WithTrackerPreflightBuilder(preflight), WithInputReadinessEvaluator(readiness))
	current := executeCommand(t, module, CreateWorkflowCommand{})
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.mkv")},
	})
	current = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"PTP", "ANT"},
	})
	if current.Projections.Status != api.StageStatusBlocked || len(current.Projections.Projections) != 2 {
		t.Fatalf("expected both question-bearing lanes: %+v", current.Projections)
	}
	current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := api.ContinueReleaseWorkflowRequest{
		Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
		IdempotencyKey: "before-config-impact",
		Goal:           api.WorkflowGoalTrackersProjected,
		Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{"PTP", "ANT"}},
	}
	retained, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil || retained.Workflow.Revision != current.Workflow.Revision {
		t.Fatalf("valid blocked questions were unnecessarily reprojected: %v", err)
	}
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := workflowStateRecord(testOwnerID, state)
	if err != nil {
		t.Fatal(err)
	}
	record, err = ApplyConfigImpact(record, api.ConfigImpactDetail{Kind: api.ConfigImpactTrackers, TrackerIDs: []api.TrackerID{"PTP"}})
	if err != nil {
		t.Fatal(err)
	}
	invalidated, err := decodeWorkflowState(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(t.Context(), testOwnerID, current.Workflow.Revision, invalidated); err != nil {
		t.Fatal(err)
	}
	current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(normalizeContinuationTrackerIDs(current.Selection.TrackerIDs), []api.TrackerID{"ANT", "PTP"}) || current.Projections.Status != api.StageStatusStale || len(current.Projections.Projections) != 1 || current.Projections.Projections[0].TrackerID != "ANT" {
		t.Fatal("fixture did not retain selection while filtering the invalidated PTP lane")
	}
	stale := *current.Workflow.TrackerProjections
	catalog := current.Catalog.Fingerprint
	generation := current.Release.Release.Generation
	request.Authority.ExpectedRevision = current.Workflow.Revision
	request.IdempotencyKey = "after-config-impact"
	updated, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Operation == nil || updated.Operation.Command != "project_trackers" {
		t.Fatal("pure discovery treated a stale filtered projection as complete")
	}
	waitForWorkflowOperation(t, module, current.Workflow.ID, updated.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
	updated, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Projections == nil || updated.Projections.ID == stale.ID || len(updated.Projections.Projections) != 2 || updated.Projections.Status != api.StageStatusBlocked {
		t.Fatalf("pure discovery did not restore both question-bearing lanes: %+v", updated.Projections)
	}
	if updated.Catalog.Fingerprint != catalog || updated.Release.Release.Generation != generation || prepareCalls != 1 || preflightCalls != 0 || readiness.calls != 0 || updated.Preflight != nil || updated.Dupes != nil {
		t.Fatal("local config reprojection changed preparation or ran downstream work")
	}
	request.Authority.ExpectedRevision = updated.Workflow.Revision
	request.IdempotencyKey = "after-local-reprojection"
	settled, err := module.Continue(t.Context(), testOwnerID, request)
	if err != nil || settled.Workflow.Revision != updated.Workflow.Revision {
		t.Fatalf("restored blocked questions did not settle: %v", err)
	}
}

func TestProjectionGoalRequiresCurrentCompleteLanes(t *testing.T) {
	for _, scenario := range []string{"blocked", "missing lane", "stale set", "stale lane", "extra lane", "submitted lane"} {
		t.Run(scenario, func(t *testing.T) {
			current := CommandResult{
				Workflow:               api.ReleaseWorkflow{ID: "projection-check", Revision: 3},
				Release:                &api.ReleaseSnapshot{},
				Selection:              &api.TrackerSelection{TrackerIDs: []api.TrackerID{"PTP", "ANT"}},
				ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{},
				Projections:            &api.TrackerReleaseProjectionSet{Status: api.StageStatusBlocked, Projections: []api.TrackerReleaseProjection{{TrackerID: "PTP", Readiness: api.ReadinessStatusBlocked}, {TrackerID: "ANT", Readiness: api.ReadinessStatusBlocked}}},
			}
			switch scenario {
			case "missing lane":
				current.Projections.Projections = current.Projections.Projections[1:]
			case "stale set":
				current.Projections.Status = api.StageStatusStale
			case "stale lane":
				current.Projections.Projections[0].Readiness = api.ReadinessStatusStale
			case "extra lane":
				current.Projections.Projections = append(current.Projections.Projections, api.TrackerReleaseProjection{TrackerID: "OTHER"})
			case "submitted lane":
				current.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{TrackerID: "PTP", Reason: "already_uploaded"}}
				current.Selection.TrackerIDs = []api.TrackerID{"ANT"}
				current.Projections.Projections = current.Projections.Projections[1:]
			}
			request := api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalTrackersProjected, Intent: api.WorkflowIntent{TrackerIDs: []api.TrackerID{"PTP", "ANT"}}}
			satisfied := scenario == "blocked" || scenario == "submitted lane"
			if continuationGoalReached(current, request) != satisfied {
				t.Fatal("incorrect pure-goal completion")
			}
			command, _ := planContinuationCommand(request, current, time.Now())
			if satisfied {
				if command != nil {
					t.Fatal("complete blocked questions were reprojected")
				}
			} else if _, ok := command.(ProjectTrackersCommand); !ok {
				t.Fatalf("stale/incomplete projection planned %T", command)
			}
		})
	}
}

func TestProjectionGoalPreservesAllSubmittedShortCircuit(t *testing.T) {
	current := CommandResult{Workflow: api.ReleaseWorkflow{
		Status:               api.WorkflowStatusCompleted,
		SubmissionExclusions: []api.SubmissionExclusion{{TrackerID: "PTP", Reason: "already_uploaded"}, {TrackerID: "ANT", Reason: "already_uploaded"}},
	}}
	if !continuationGoalReached(current, api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalTrackersProjected, Intent: api.WorkflowIntent{TrackerIDs: []api.TrackerID{"PTP", "ANT"}}}) {
		t.Fatal("history-only completion required submitted lanes to reappear")
	}
}
