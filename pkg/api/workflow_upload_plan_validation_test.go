// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strings"
	"testing"
	"time"
)

func TestSkippedUploadPlanContainsNoTrackerOperations(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 22, 1, 2, 3, 0, time.UTC)
	plan := UploadPlan{
		ID:               "plan-1",
		WorkflowID:       "workflow-1",
		Revision:         1,
		Release:          ReleaseSnapshotRef{ID: "release-1", Revision: 1},
		ReleaseRef:       ReleaseRef{SourcePath: "Example.Release.2026", Generation: 1},
		ProjectionSet:    TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 1},
		Dupes:            DupeAssessmentRef{ID: "dupes-1", Revision: 1},
		Media:            &MediaArtifactSetRef{ID: "media-1", Revision: 1},
		Descriptions:     &DescriptionSetRef{ID: "descriptions-1", Revision: 1},
		InputFingerprint: workflowTestFingerprint(t, "skipped-upload-plan"),
		Status:           StageStatusSkipped,
		SingleUse:        true,
		CreatedAt:        createdAt,
		ExpiresAt:        createdAt.Add(time.Minute),
	}

	if err := plan.Validate(); err != nil {
		t.Fatalf("validate empty skipped upload plan: %v", err)
	}
	plan.Trackers = []UploadPlanTracker{{
		TrackerID:           "AITHER",
		UploadReleaseName:   "Example.Release.2026.AITHER-GRP",
		Status:              StageStatusSkipped,
		SemanticFingerprint: workflowTestFingerprint(t, "skipped-aither"),
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "cannot contain tracker operations") {
		t.Fatalf("validate populated skipped upload plan error = %v", err)
	}
}
