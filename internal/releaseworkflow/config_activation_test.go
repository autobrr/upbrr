// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestConfigActivationGuardRejectsStaleAdmission(t *testing.T) {
	stale := errors.New("stale config generation")
	module, _ := newTestModule(t, testPreparer(), WithConfigActivationGuard(func(context.Context) error { return stale }))
	_, err := module.Execute(t.Context(), testOwnerID, CreateWorkflowCommand{})
	if !errors.Is(err, stale) {
		t.Fatalf("stale generation admission error = %v", err)
	}
}

func TestApplyConfigImpactDescriptionPreservesDupeAndMedia(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fingerprint := testFingerprint(t, "config-activation-failed-upload")
	dupes := api.DupeAssessmentRef{ID: "dupes", Revision: 1}
	media := api.MediaArtifactSetRef{ID: "media", Revision: 1}
	descriptions := api.DescriptionSetRef{ID: "descriptions", Revision: 1}
	uploadResult := api.UploadResultRef{ID: "upload", Revision: 4}
	release := api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	catalog := api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1}
	runtime := api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1}
	selection := api.TrackerSelectionRef{ID: "selection", Revision: 1}
	projections := api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
	state := State{
		OwnerID: "owner",
		Workflow: api.ReleaseWorkflow{
			ID:                 "workflow",
			Revision:           4,
			FactInstructions:   api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
			Release:            &release,
			TrackerCatalog:     &catalog,
			TrackerRuntime:     &runtime,
			Selection:          &selection,
			TrackerProjections: &projections,
			Status:             api.WorkflowStatusCompleted,
			Dupes:              &dupes,
			Media:              &media,
			Descriptions:       &descriptions,
			UploadResult:       &uploadResult,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		UploadResults: map[api.UploadResultID]api.UploadResult{
			"upload": {
				ID:               "upload",
				WorkflowID:       "workflow",
				Revision:         4,
				ProjectionSet:    projections,
				Dupes:            dupes,
				Media:            media,
				Descriptions:     descriptions,
				InputFingerprint: fingerprint,
				Results: []api.UploadTrackerResult{{
					TrackerID:             "ALPHA",
					Status:                api.StageStatusFailed,
					SubmissionStatus:      api.StageStatusFailed,
					ClientInjectionStatus: api.StageStatusPending,
					Failures: []api.WorkflowFailure{{
						Failure: api.OperationFailure{
							Code:      api.OperationFailureInternal,
							Operation: api.OperationKindUploadExecute,
							Message:   "Synthetic upload failure.",
							Recovery:  api.OperationRecoveryRetry,
						},
						TrackerID: "ALPHA",
					}},
				}},
				Status:    api.StageStatusFailed,
				CreatedAt: now,
			},
		},
	}
	record, err := workflowStateRecord("owner", state)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplyConfigImpact(record, api.ConfigImpactDetail{Kind: api.ConfigImpactDescription})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeWorkflowState(updated)
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.Revision != 5 || result.Workflow.Descriptions != nil || result.Workflow.DryRun != nil ||
		result.Workflow.UploadResult != nil || result.Workflow.Status != api.WorkflowStatusActive {
		t.Fatalf("updated workflow = %#v", result.Workflow)
	}
	if result.Workflow.Dupes == nil || result.Workflow.Media == nil {
		t.Fatalf("description impact discarded reusable dependencies: %#v", result.Workflow)
	}
}

func TestApplyConfigImpactTrackerLanesPreservesUnaffectedProjection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	release := api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	catalog := api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1}
	runtime := api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1}
	selection := api.TrackerSelectionRef{ID: "selection", Revision: 1}
	projections := api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
	state := State{
		OwnerID: "owner",
		Workflow: api.ReleaseWorkflow{
			ID:                 "workflow",
			Revision:           4,
			FactInstructions:   api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
			Release:            &release,
			TrackerCatalog:     &catalog,
			TrackerRuntime:     &runtime,
			Selection:          &selection,
			TrackerProjections: &projections,
			Status:             api.WorkflowStatusActive,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{
			"projections": {
				ID:          "projections",
				Revision:    1,
				Projections: []api.TrackerReleaseProjection{{TrackerID: "ALPHA"}, {TrackerID: "BETA"}},
			},
		},
	}
	record, err := workflowStateRecord("owner", state)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplyConfigImpact(record, api.ConfigImpactDetail{Kind: api.ConfigImpactTrackers, TrackerIDs: []api.TrackerID{" alpha ", "ALPHA", " "}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeWorkflowState(updated)
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.TrackerProjections == nil || result.Workflow.TrackerProjections.ID == "projections" {
		t.Fatalf("tracker projections = %#v, want a filtered revision", result.Workflow.TrackerProjections)
	}
	filtered := result.Projections[result.Workflow.TrackerProjections.ID]
	if len(filtered.Projections) != 1 || filtered.Projections[0].TrackerID != "BETA" {
		t.Fatalf("filtered projections = %#v", filtered.Projections)
	}
}

func TestApplyConfigImpactTrackerLanesPreservesHistoricalAssessments(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	release := api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	catalog := api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1}
	runtime := api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1}
	selection := api.TrackerSelectionRef{ID: "selection", Revision: 1}
	projections := api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
	preflight := api.TrackerPreflightAssessmentRef{ID: "preflight", Revision: 1}
	dupes := api.DupeAssessmentRef{ID: "dupes", Revision: 1}
	state := State{
		OwnerID: "owner",
		Workflow: api.ReleaseWorkflow{
			ID:                 "workflow",
			Revision:           4,
			FactInstructions:   api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
			Release:            &release,
			TrackerCatalog:     &catalog,
			TrackerRuntime:     &runtime,
			Selection:          &selection,
			TrackerProjections: &projections,
			TrackerPreflight:   &preflight,
			Dupes:              &dupes,
			Status:             api.WorkflowStatusActive,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{
			"projections": {
				ID:       "projections",
				Revision: 1,
				Projections: []api.TrackerReleaseProjection{
					{TrackerID: "ALPHA"},
					{TrackerID: "BETA"},
				},
				RequiredActions: []api.RequiredAction{
					{TrackerID: "ALPHA"},
					{TrackerID: "BETA"},
				},
				Failures: []api.WorkflowFailure{
					{TrackerID: "ALPHA"},
					{TrackerID: "BETA"},
				},
			},
		},
		Preflights: map[api.TrackerPreflightAssessmentID]api.TrackerPreflightAssessment{
			"preflight": {
				ID:       "preflight",
				Revision: 1,
				Results: []api.TrackerPreflightResult{
					{TrackerID: "ALPHA"},
					{TrackerID: "BETA"},
				},
			},
		},
		Dupes: map[api.DupeAssessmentID]api.DupeAssessment{
			"dupes": {
				ID:       "dupes",
				Revision: 1,
				Results: []api.TrackerDupeAssessment{
					{TrackerID: "ALPHA"},
					{TrackerID: "BETA"},
				},
			},
		},
	}
	record, err := workflowStateRecord("owner", state)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplyConfigImpact(record, api.ConfigImpactDetail{Kind: api.ConfigImpactTrackers, TrackerIDs: []api.TrackerID{"ALPHA"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeWorkflowState(updated)
	if err != nil {
		t.Fatal(err)
	}
	if historical := result.Projections["projections"].Projections; len(historical) != 2 || historical[0].TrackerID != "ALPHA" || historical[1].TrackerID != "BETA" {
		t.Fatalf("historical projections = %#v", historical)
	}
	if historical := result.Projections["projections"].RequiredActions; len(historical) != 2 || historical[0].TrackerID != "ALPHA" || historical[1].TrackerID != "BETA" {
		t.Fatalf("historical projection actions = %#v", historical)
	}
	if historical := result.Projections["projections"].Failures; len(historical) != 2 || historical[0].TrackerID != "ALPHA" || historical[1].TrackerID != "BETA" {
		t.Fatalf("historical projection failures = %#v", historical)
	}
	filtered := result.Projections[result.Workflow.TrackerProjections.ID]
	if len(filtered.RequiredActions) != 1 || filtered.RequiredActions[0].TrackerID != "BETA" {
		t.Fatalf("filtered projection actions = %#v", filtered.RequiredActions)
	}
	if len(filtered.Failures) != 1 || filtered.Failures[0].TrackerID != "BETA" {
		t.Fatalf("filtered projection failures = %#v", filtered.Failures)
	}
	if historical := result.Preflights["preflight"].Results; len(historical) != 2 || historical[0].TrackerID != "ALPHA" || historical[1].TrackerID != "BETA" {
		t.Fatalf("historical preflight = %#v", historical)
	}
	if historical := result.Dupes["dupes"].Results; len(historical) != 2 || historical[0].TrackerID != "ALPHA" || historical[1].TrackerID != "BETA" {
		t.Fatalf("historical dupes = %#v", historical)
	}
}

func TestApplyConfigImpactImageHostingWithdrawsCurrentMedia(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	release := api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	catalog := api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1}
	runtime := api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1}
	selection := api.TrackerSelectionRef{ID: "selection", Revision: 1}
	projections := api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
	media := api.MediaArtifactSetRef{ID: "media", Revision: 1}
	state := State{
		OwnerID: "owner",
		Workflow: api.ReleaseWorkflow{
			ID:                 "workflow",
			Revision:           4,
			FactInstructions:   api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
			Release:            &release,
			TrackerCatalog:     &catalog,
			TrackerRuntime:     &runtime,
			Selection:          &selection,
			TrackerProjections: &projections,
			Media:              &media,
			Status:             api.WorkflowStatusActive,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		Media: map[api.MediaArtifactSetID]api.MediaArtifactSet{"media": {ID: "media", Revision: 1}},
	}
	record, err := workflowStateRecord("owner", state)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplyConfigImpact(record, api.ConfigImpactDetail{Kind: api.ConfigImpactImageHosting})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeWorkflowState(updated)
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.Media != nil || result.Media["media"].ID != "media" {
		t.Fatalf("host impact media authority = %#v; retained media = %#v", result.Workflow.Media, result.Media)
	}
}
