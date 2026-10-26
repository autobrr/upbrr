// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowNamingFailureSkipsOnlyAffectedTracker(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	registry := trackers.NewRegistry()
	for _, trackerID := range []string{"ALPHA", "BETA"} {
		descriptor := trackers.Descriptor{Name: trackerID, Definition: workflowImageHostPolicyDefinition{name: trackerID}}
		if trackerID == "ALPHA" {
			descriptor.ReleaseNamePolicy = trackers.StructuredReleaseNamePolicy("test/required-name/v1", trackers.StructuredNamePolicy{
				Defaults: func(*trackers.NameEditor, api.UploadSubject, config.TrackerConfig) error {
					return &trackers.NameRuleError{Rule: "required_title", Reason: "Required tracker title is unavailable"}
				},
			})
		}
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	subject := api.UploadSubject{
		SourcePath:  source,
		ReleaseName: "Example Release 2026 1080p WEB-DL-GRP",
		Type:        "WEBDL",
		Source:      "Web",
	}
	subject.GeneratedName = &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{{
		Role:    api.NameRoleTitle,
		Value:   "Example Release",
		Present: true,
	}}}
	subject.ReleaseName = subject.GeneratedName.Render().Name
	catalog, runtime, _, projections, err := projector.Build(ctx, api.ReleaseSnapshot{}, subject, []api.TrackerID{"ALPHA", "BETA"}, nil, nil, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	if projections.Status != api.StageStatusReady || projections.Projections[0].UploadReleaseName != "" || projections.Projections[0].DupeReady {
		t.Fatalf("naming failure did not stay tracker-scoped: %+v", projections)
	}
	projections.ID, projections.Revision = "projections-1", 1
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	preflight, finalized, err := (workflowPreflightBuilder{auth: workflowPreflightAuthFake{}, registry: registry}).Build(ctx, subject, catalog, runtime, projections, now)
	if err != nil {
		t.Fatal(err)
	}
	preflight.ID, preflight.Revision = "preflight-1", 1
	if preflight.Results[1].State != api.TrackerPreflightStateReady {
		t.Fatalf("healthy sibling failed preflight: %+v", preflight.Results[1])
	}
	preflight.Status = api.StageStatusReady
	projections.Projections = finalized
	service := &workflowDupeServiceFake{results: []api.DupeCheckResult{{
		Tracker: "BETA",
		Status:  "completed",
		Search:  api.DupeSearchEvidence{Complete: true},
	}}}
	assessment, _, err := (workflowDupeBuilder{service: service}).Build(ctx, api.DuplicateSubject{SourcePath: source}, projections, preflight, now, false)
	if err != nil {
		t.Fatal(err)
	}
	assessment.ID, assessment.WorkflowID, assessment.Revision = "dupes-1", "workflow-1", 1
	assessment.Release = api.ReleaseSnapshotRef{ID: "release-1", Revision: 1}
	assessment.ReleaseRef = api.ReleaseRef{SourcePath: source, Generation: 1}
	assessment.Selection = api.TrackerSelectionRef{ID: "selection-1", Revision: 1}
	assessment.ProjectionSet = api.TrackerReleaseProjectionSetRef{ID: projections.ID, Revision: projections.Revision}
	assessment.CreatedAt = now
	assessment.Status = api.StageStatusCompleted
	if err := assessment.Validate(); err != nil {
		t.Fatalf("naming failure prevented duplicate publication: %v", err)
	}
	if len(service.projections) != 1 || service.projections[0].TrackerID != "BETA" || len(assessment.Results) != 2 ||
		assessment.Results[0].Decision != api.DupeDecisionSkipped || len(assessment.Results[0].Failures) == 0 ||
		assessment.Results[1].Decision != api.DupeDecisionNoMatch {
		t.Fatalf("unexpected mixed duplicate results: %+v", assessment)
	}
	for index, projection := range finalized {
		if eligible := releaseworkflow.ProjectionEligibleForDownstream(projection, assessment.Results[index], true); eligible != (projection.TrackerID == "BETA") {
			t.Fatalf("tracker %s downstream eligibility=%t", projection.TrackerID, eligible)
		}
	}
	service.results = append(service.results, api.DupeCheckResult{
		Tracker:  "ALPHA",
		Status:   "completed",
		HasDupes: true,
		Search: api.DupeSearchEvidence{
			Complete:       true,
			CandidateCount: 1,
			Scope:          "local_client",
		},
		Evaluations: []api.DupeCandidateEvaluation{{
			Name:     subject.ReleaseName,
			Relation: api.DupeRelationExactDuplicate,
			Reasons:  []api.DupeReason{{Code: "in_client"}},
		}},
	})
	inClient, _, err := (workflowDupeBuilder{service: service}).Build(ctx, api.DuplicateSubject{
		SourcePath:      source,
		ReleaseName:     subject.ReleaseName,
		MatchedTrackers: []string{"ALPHA"},
	}, projections, preflight, now, false)
	if err != nil {
		t.Fatal(err)
	}
	assessment.Results = inClient.Results
	if err := assessment.Validate(); err != nil {
		t.Fatalf("in-client naming failure prevented sibling publication: %v", err)
	}
	if assessment.Results[0].Decision != api.DupeDecisionAccepted || len(assessment.Results[0].Matches) != 1 ||
		assessment.Results[0].Matches[0].Reason != "in_client" {
		t.Fatalf("naming failure lost strict client evidence: %+v", assessment.Results[0])
	}
	for index, projection := range finalized {
		if eligible := releaseworkflow.ProjectionEligibleForDownstream(projection, assessment.Results[index], true); eligible != (projection.TrackerID == "BETA") {
			t.Fatalf("in-client tracker %s downstream eligibility=%t", projection.TrackerID, eligible)
		}
	}
}
