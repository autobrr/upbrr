// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"testing"
	"time"
)

func TestDupeAssessmentAllowsMissingNameForNonUploadableTracker(t *testing.T) {
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	fp := workflowTestFingerprint(t, "skipped-naming")
	assessment := DupeAssessment{
		ID:               "dupes-1",
		WorkflowID:       "workflow-1",
		Revision:         1,
		Release:          ReleaseSnapshotRef{ID: "release-1", Revision: 1},
		ReleaseRef:       ReleaseRef{SourcePath: "synthetic.mkv", Generation: 1},
		Selection:        TrackerSelectionRef{ID: "selection-1", Revision: 1},
		ProjectionSet:    TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1},
		InputFingerprint: fp,
		Status:           StageStatusCompleted,
		CreatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
		Results: []TrackerDupeAssessment{{
			TrackerID:             "EXAMPLE",
			ProjectionFingerprint: fp,
			CriteriaFingerprint:   fp,
			Decision:              DupeDecisionSkipped,
			Status:                StageStatusSkipped,
			CheckedAt:             now,
			FreshUntil:            now.Add(time.Hour),
		}},
	}
	if err := assessment.Validate(); err != nil {
		t.Fatalf("skipped naming failure invalidated assessment: %v", err)
	}
	for _, decision := range []DupeDecision{DupeDecisionNoMatch, DupeDecisionAccepted, DupeDecisionIgnored, DupeDecisionPending, DupeDecisionBypassed} {
		assessment.Results[0].Decision = decision
		if err := assessment.Validate(); err == nil {
			t.Fatalf("unnamed result accepted with decision %s", decision)
		}
	}
	assessment.Results[0].Decision = DupeDecisionSkipped
	assessment.Results[0].Status = StageStatusCompleted
	if err := assessment.Validate(); err == nil {
		t.Fatal("unnamed completed result was accepted")
	}
	assessment.Results[0].Status = StageStatusSkipped
	assessment.Results[0].TrackerID = ""
	if err := assessment.Validate(); err == nil {
		t.Fatal("skipped result without tracker identity was accepted")
	}
	assessment.Results[0].TrackerID = "EXAMPLE"
	assessment.Results[0].Decision = DupeDecisionAccepted
	assessment.Results[0].Status = StageStatusCompleted
	assessment.Results[0].Matches = []DupeMatchProjection{{
		Name:     "Example Release",
		Reason:   "in_client",
		Relation: DupeRelationExactDuplicate,
	}}
	if err := assessment.Validate(); err != nil {
		t.Fatalf("accepted duplicate without an upload name invalidated assessment: %v", err)
	}
	assessment.Results[0].Decision = DupeDecisionIgnored
	if err := assessment.Validate(); err == nil {
		t.Fatal("ignored duplicate without an upload name was accepted")
	}
}
