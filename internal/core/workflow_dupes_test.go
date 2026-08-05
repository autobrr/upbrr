// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type workflowDupeServiceFake struct {
	queries []string
	result  *api.DupeCheckResult
}

type workflowProjectionDupeServiceFake struct {
	options api.ProjectionDupeCheckOptions
}

func (*workflowProjectionDupeServiceFake) Check(
	context.Context,
	api.DuplicateSubject,
	[]string,
) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, errors.New("unexpected legacy duplicate check")
}

func (f *workflowProjectionDupeServiceFake) CheckProjectionSet(
	_ context.Context,
	subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet,
	options api.ProjectionDupeCheckOptions,
) (api.DupeCheckSummary, api.DupeAssessmentEvidence, error) {
	f.options = options
	return api.DupeCheckSummary{
		SourcePath: subject.SourcePath,
		Results: []api.DupeCheckResult{{
			Tracker:    string(projections.Projections[0].TrackerID),
			Skipped:    true,
			SkipCode:   dupechecking.NotRunBannedGroup,
			SkipReason: "debug mode bypassed policy: group GRP is banned",
			Status:     "bypassed",
		}},
	}, dupechecking.EmptyAssessment(), nil
}

func (f *workflowDupeServiceFake) Check(
	_ context.Context,
	subject api.DuplicateSubject,
	trackerIDs []string,
) (api.DupeCheckSummary, error) {
	f.queries = append(f.queries, subject.ReleaseName)
	if f.result != nil {
		result := *f.result
		result.Tracker = trackerIDs[0]
		return api.DupeCheckSummary{SourcePath: subject.SourcePath, Results: []api.DupeCheckResult{result}}, nil
	}
	return api.DupeCheckSummary{
		SourcePath: subject.SourcePath,
		Results: []api.DupeCheckResult{{
			Tracker: trackerIDs[0],
			Evaluations: []api.DupeCandidateEvaluation{{
				ID:       "123",
				Name:     "Existing.Query.Name",
				Link:     "https://tracker.invalid/123",
				Relation: api.DupeRelationExactDuplicate,
				Reasons:  []api.DupeReason{{Code: "exact_identity"}},
			}},
			HasDupes: true,
			Status:   "completed",
		}},
	}, nil
}

func (f *workflowDupeServiceFake) CheckProjectionSet(
	ctx context.Context,
	subject api.DuplicateSubject,
	projections api.TrackerReleaseProjectionSet,
	_ api.ProjectionDupeCheckOptions,
) (api.DupeCheckSummary, api.DupeAssessmentEvidence, error) {
	trackers := make([]string, 0, len(projections.Projections))
	for _, projection := range projections.Projections {
		trackers = append(trackers, string(projection.TrackerID))
	}
	if len(projections.Projections) > 0 {
		subject.ReleaseName = projections.Projections[0].DuplicateCriteria.Name
	}
	summary, err := f.Check(ctx, subject, trackers)
	return summary, dupechecking.EmptyAssessment(), err
}

func TestWorkflowDupeBuilderReportsDebugBannedGroupAsBypassed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	criteria := api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p-GRP"}
	criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(criteria)
	if err != nil {
		t.Fatalf("criteria fingerprint: %v", err)
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:            "ALPHA",
		DisplayName:          "Alpha",
		CanonicalReleaseName: criteria.Name,
		UploadReleaseName:    criteria.Name,
		DuplicateCriteria:    criteria,
		InputFingerprint:     workflowTestFingerprint(t, "debug-bypass-input"),
		CatalogFingerprint:   workflowTestFingerprint(t, "debug-bypass-catalog"),
		ConfigFingerprint:    workflowTestFingerprint(t, "debug-bypass-config"),
		ProjectorFingerprint: workflowTestFingerprint(t, "debug-bypass-projector"),
		CriteriaFingerprint:  criteriaFingerprint,
		Readiness:            api.ReadinessStatusReady,
		DupeReady:            true,
		UploadReady:          true,
	}
	projections := api.TrackerReleaseProjectionSet{
		ID:            "projections-debug",
		Revision:      4,
		ExecutionMode: api.WorkflowExecutionModeDebug,
		Projections:   []api.TrackerReleaseProjection{projection},
	}
	service := &workflowProjectionDupeServiceFake{}
	snapshot, _, err := (workflowDupeBuilder{service: service}).Build(
		context.Background(),
		api.DuplicateSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Release.2026.mkv")},
		projections,
		api.TrackerPreflightAssessment{
			ID:        "preflight-debug",
			Revision:  5,
			Status:    api.StageStatusReady,
			ExpiresAt: now.Add(time.Hour),
			Results: []api.TrackerPreflightResult{{
				TrackerID: "ALPHA",
				State:     api.TrackerPreflightStateReady,
			}},
		},
		now,
		false,
	)
	if err != nil {
		t.Fatalf("build debug bypassed dupes: %v", err)
	}
	if !service.options.BypassBannedGroups {
		t.Fatal("debug duplicate check did not carry banned-group bypass authority")
	}
	if len(snapshot.Results) != 1 || snapshot.Results[0].Decision != api.DupeDecisionBypassed ||
		snapshot.Results[0].Status != api.StageStatusCompleted {
		t.Fatalf("debug bypassed duplicate snapshot = %#v", snapshot.Results)
	}
}

func TestWorkflowDupeBuilderUsesProjectionCriteriaAndRetainsSafeUploadName(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	criteria := api.TrackerDuplicateCriteria{Name: "Query.Name.2026-GRP"}
	criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(criteria)
	if err != nil {
		t.Fatalf("criteria fingerprint: %v", err)
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:            "ALPHA",
		DisplayName:          "Alpha",
		CanonicalReleaseName: "Canonical.Name.2026-GRP",
		UploadReleaseName:    "Upload.Name.2026-GRP",
		DuplicateCriteria:    criteria,
		InputFingerprint:     workflowTestFingerprint(t, "input"),
		CatalogFingerprint:   workflowTestFingerprint(t, "catalog"),
		ConfigFingerprint:    workflowTestFingerprint(t, "config"),
		ProjectorFingerprint: workflowTestFingerprint(t, "projector"),
		CriteriaFingerprint:  criteriaFingerprint,
		Readiness:            api.ReadinessStatusReady,
		DupeReady:            true,
		UploadReady:          true,
	}
	projectionSet := api.TrackerReleaseProjectionSet{
		ID:          "projections-1",
		Revision:    4,
		Projections: []api.TrackerReleaseProjection{projection},
	}
	preflight := api.TrackerPreflightAssessment{
		ID:        "preflight-1",
		Revision:  5,
		Status:    api.StageStatusReady,
		ExpiresAt: now.Add(time.Hour),
		Results: []api.TrackerPreflightResult{{
			TrackerID: "ALPHA",
			State:     api.TrackerPreflightStateReady,
		}},
	}
	service := &workflowDupeServiceFake{}
	snapshot, privateEvidence, err := (workflowDupeBuilder{service: service}).Build(
		context.Background(),
		api.DuplicateSubject{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
		projectionSet,
		preflight,
		now,
		false,
	)
	if err != nil {
		t.Fatalf("build workflow dupes: %v", err)
	}
	if len(service.queries) != 1 || service.queries[0] != criteria.Name {
		t.Fatalf("duplicate queries = %v", service.queries)
	}
	if len(snapshot.Results) != 1 || snapshot.Results[0].UploadReleaseName != projection.UploadReleaseName ||
		snapshot.Results[0].Criteria.Name != criteria.Name || snapshot.Results[0].Decision != api.DupeDecisionAccepted ||
		snapshot.Results[0].Status != api.StageStatusCompleted || len(snapshot.Results[0].RequiredActions) != 0 {
		t.Fatalf("duplicate snapshot = %#v", snapshot)
	}
	if privateEvidence == nil {
		t.Fatal("duplicate private evidence was not retained")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal duplicate snapshot: %v", err)
	}
	if strings.Contains(string(payload), "token=secret") {
		t.Fatalf("public duplicate snapshot exposed download authority: %s", payload)
	}
}

func TestWorkflowDupeBuilderRetainsStrictInClientEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	criteria := api.TrackerDuplicateCriteria{Name: "Example.Release.2026.1080p-GRP"}
	criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(criteria)
	if err != nil {
		t.Fatalf("criteria fingerprint: %v", err)
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:            "EXAMPLE",
		DisplayName:          "Example",
		CanonicalReleaseName: criteria.Name,
		UploadReleaseName:    criteria.Name,
		DuplicateCriteria:    criteria,
		InputFingerprint:     workflowTestFingerprint(t, "input-in-client"),
		CatalogFingerprint:   workflowTestFingerprint(t, "catalog-in-client"),
		ConfigFingerprint:    workflowTestFingerprint(t, "config-in-client"),
		ProjectorFingerprint: workflowTestFingerprint(t, "projector-in-client"),
		CriteriaFingerprint:  criteriaFingerprint,
		Readiness:            api.ReadinessStatusReady,
		DupeReady:            true,
		UploadReady:          true,
	}
	service := &workflowDupeServiceFake{result: &api.DupeCheckResult{
		HasDupes: true,
		Evaluations: []api.DupeCandidateEvaluation{{
			ID:       "456",
			Name:     criteria.Name,
			Link:     "https://tracker.invalid/456",
			Relation: api.DupeRelationExactDuplicate,
			Reasons:  []api.DupeReason{{Code: "in_client"}},
		}},
		Status: "completed",
	}}
	snapshot, _, err := (workflowDupeBuilder{service: service}).Build(
		context.Background(),
		api.DuplicateSubject{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
		api.TrackerReleaseProjectionSet{
			ID:          "projections-1",
			Revision:    4,
			Projections: []api.TrackerReleaseProjection{projection},
		},
		api.TrackerPreflightAssessment{
			ID:        "preflight-1",
			Revision:  5,
			Status:    api.StageStatusReady,
			ExpiresAt: now.Add(time.Hour),
			Results:   []api.TrackerPreflightResult{{TrackerID: "EXAMPLE", State: api.TrackerPreflightStateReady}},
		},
		now,
		false,
	)
	if err != nil {
		t.Fatalf("build in-client workflow dupes: %v", err)
	}
	if len(snapshot.Results) != 1 || snapshot.Results[0].Decision != api.DupeDecisionAccepted ||
		snapshot.Results[0].Status != api.StageStatusCompleted || len(snapshot.Results[0].Matches) != 1 ||
		snapshot.Results[0].Matches[0].Reason != "in_client" || len(snapshot.Results[0].RequiredActions) != 0 {
		t.Fatalf("in-client duplicate snapshot = %#v", snapshot)
	}
}

func TestWorkflowDupeBuilderHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := (workflowDupeBuilder{service: &workflowDupeServiceFake{}}).Build(
		ctx,
		api.DuplicateSubject{},
		api.TrackerReleaseProjectionSet{},
		api.TrackerPreflightAssessment{},
		time.Now(),
		false,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled duplicate build error = %v", err)
	}
}

func TestDuplicateEvidenceFingerprintChangesWithCandidateEvaluation(t *testing.T) {
	t.Parallel()

	result := api.DupeCheckResult{
		Status: "completed",
		Search: api.DupeSearchEvidence{
			Complete:       true,
			Pages:          1,
			CandidateCount: 1,
		},
		Evaluations: []api.DupeCandidateEvaluation{{
			ID:       "candidate-1",
			Name:     "Example.Release.2026.1080p-GRP",
			Relation: api.DupeRelationSameSlot,
			Reasons:  []api.DupeReason{{Code: "same_tracker_slot"}},
			HDR:      api.HDRFacts{Origin: api.HDREvidenceTrackerAPI, Status: api.HDREvidenceComplete},
		}},
	}
	first, err := duplicateEvidenceFingerprint(result)
	if err != nil {
		t.Fatalf("first evidence fingerprint: %v", err)
	}
	result.Evaluations[0].Relation = api.DupeRelationCoexists
	result.Evaluations[0].Reasons = []api.DupeReason{{Code: "different_source"}}
	second, err := duplicateEvidenceFingerprint(result)
	if err != nil {
		t.Fatalf("second evidence fingerprint: %v", err)
	}
	if first == second {
		t.Fatal("candidate evaluation did not change evidence fingerprint")
	}
}

func TestWorkflowDupeOutcomeIncompleteSearchWithoutCandidatesRequiresReview(t *testing.T) {
	t.Parallel()

	result := api.DupeCheckResult{
		UploadReleaseName: "Example.Release.2026.1080p-GRP",
		Status:            "completed",
		Search: api.DupeSearchEvidence{
			Complete: false,
			Pages:    2,
		},
	}
	target := api.TrackerDupeAssessment{
		TrackerID: "EXAMPLE",
		Matches:   publicDupeMatches(result),
	}
	setWorkflowDupeOutcome(&target, result)
	if target.Decision != api.DupeDecisionPending || target.Status != api.StageStatusBlocked ||
		len(target.RequiredActions) != 1 {
		t.Fatalf("incomplete empty search outcome = %#v", target)
	}
	if len(target.Matches) != 1 || target.Matches[0].Name != result.UploadReleaseName ||
		target.Matches[0].Reason != "incomplete_search" || target.Matches[0].Relation != api.DupeRelationInsufficientEvidence ||
		len(target.Matches[0].Reasons) != 1 || target.Matches[0].Reasons[0].Code != "incomplete_search" {
		t.Fatalf("incomplete empty search matches = %#v", target.Matches)
	}
	result.Search.Complete = true
	if matches := publicDupeMatches(result); len(matches) != 0 {
		t.Fatalf("complete empty search matches = %#v", matches)
	}
	action := target.RequiredActions[0]
	if action.Kind != api.RequiredActionReviewDuplicates ||
		action.Status != api.RequiredActionStatusPending ||
		action.TrackerID != target.TrackerID ||
		action.Prompt != "Review incomplete, same-slot, or proposed-trump duplicate evidence and acknowledge tracker policy risk." ||
		len(action.Options) != 2 ||
		action.Options[0] != (api.RequiredActionOption{Value: string(api.DupeDecisionAccepted), Label: "Treat as duplicate"}) ||
		action.Options[1] != (api.RequiredActionOption{Value: string(api.DupeDecisionIgnored), Label: "Acknowledge risk and continue"}) {
		t.Fatalf("incomplete empty search action = %#v", action)
	}
}

func TestWorkflowDupeBuilderSearchesReadySiblingAndRetainsAuthBlockedRow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	projection := func(trackerID api.TrackerID, readiness api.ReadinessStatus, dupeReady bool) api.TrackerReleaseProjection {
		criteria := api.TrackerDuplicateCriteria{Name: "Example.Release.2026." + string(trackerID) + "-GRP"}
		criteriaFingerprint, err := api.CanonicalWorkflowFingerprint(criteria)
		if err != nil {
			t.Fatalf("criteria fingerprint: %v", err)
		}
		return api.TrackerReleaseProjection{
			TrackerID:            trackerID,
			DisplayName:          string(trackerID),
			CanonicalReleaseName: criteria.Name,
			UploadReleaseName:    criteria.Name,
			DuplicateCriteria:    criteria,
			InputFingerprint:     workflowTestFingerprint(t, string(trackerID)+"-input"),
			CatalogFingerprint:   workflowTestFingerprint(t, string(trackerID)+"-catalog"),
			ConfigFingerprint:    workflowTestFingerprint(t, string(trackerID)+"-config"),
			ProjectorFingerprint: workflowTestFingerprint(t, string(trackerID)+"-projector"),
			CriteriaFingerprint:  criteriaFingerprint,
			Readiness:            readiness,
			DupeReady:            dupeReady,
			UploadReady:          dupeReady,
		}
	}
	ready := projection("ALPHA", api.ReadinessStatusReady, true)
	authBlocked := projection("BETA", api.ReadinessStatusBlocked, false)
	authBlocked.Failures = []api.WorkflowFailure{{
		TrackerID: "BETA",
		Failure: api.OperationFailure{
			Code:      api.OperationFailureTrackerAuthRequired,
			Operation: api.OperationKindDuplicateCheck,
			Message:   "Tracker authentication is not ready for this attempt.",
			Recovery:  api.OperationRecoveryAuthenticateTrackers,
		},
	}}
	preflight := api.TrackerPreflightAssessment{
		ID:        "preflight-1",
		Revision:  5,
		Status:    api.StageStatusReady,
		ExpiresAt: now.Add(time.Hour),
		Results: []api.TrackerPreflightResult{
			{TrackerID: "ALPHA", State: api.TrackerPreflightStateReady},
			{
				TrackerID: "BETA",
				State:     api.TrackerPreflightStateRetryable,
				Failures:  authBlocked.Failures,
			},
		},
	}
	service := &workflowDupeServiceFake{}
	snapshot, _, err := (workflowDupeBuilder{service: service}).Build(
		context.Background(),
		api.DuplicateSubject{SourcePath: "C:\\releases\\Example.Release.2026.mkv"},
		api.TrackerReleaseProjectionSet{
			ID:          "projections-1",
			Revision:    4,
			Projections: []api.TrackerReleaseProjection{ready, authBlocked},
		},
		preflight,
		now,
		false,
	)
	if err != nil {
		t.Fatalf("build mixed workflow dupes: %v", err)
	}
	if len(service.queries) != 1 || service.queries[0] != ready.DuplicateCriteria.Name {
		t.Fatalf("eligible duplicate queries = %v", service.queries)
	}
	if len(snapshot.Results) != 2 || snapshot.Results[1].TrackerID != "BETA" ||
		snapshot.Results[1].Decision != api.DupeDecisionSkipped || snapshot.Results[1].Status != api.StageStatusSkipped ||
		len(snapshot.Results[1].Failures) != 1 {
		t.Fatalf("mixed duplicate snapshot = %#v", snapshot)
	}
}

func workflowTestFingerprint(t *testing.T, value string) api.WorkflowFingerprint {
	t.Helper()
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint %s: %v", value, err)
	}
	return fingerprint
}
