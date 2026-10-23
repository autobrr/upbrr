// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"testing"
	"time"
)

func TestDupeAssessmentPolicyOnlyReview(t *testing.T) {
	t.Parallel()
	for _, decision := range []DupeDecision{DupeDecisionAccepted, DupeDecisionIgnored} {
		t.Run(string(decision), func(t *testing.T) {
			for _, complete := range []bool{true, false} {
				now := time.Date(2026, time.October, 23, 0, 0, 0, 0, time.UTC)
				fp := workflowTestFingerprint(t, "policy-only-review")
				assessment := DupeAssessment{
					ID:               "dupes-policy",
					WorkflowID:       "workflow-policy",
					Revision:         1,
					Release:          ReleaseSnapshotRef{ID: "release-policy", Revision: 1},
					ReleaseRef:       ReleaseRef{SourcePath: "synthetic.mkv", Generation: 1},
					Selection:        TrackerSelectionRef{ID: "selection-policy", Revision: 1},
					ProjectionSet:    TrackerReleaseProjectionSetRef{ID: "projections-policy", Revision: 1},
					InputFingerprint: fp,
					Status:           StageStatusCompleted,
					CreatedAt:        now,
					ExpiresAt:        now.Add(time.Hour),
					Results: []TrackerDupeAssessment{{
						TrackerID:             "AITHER",
						UploadReleaseName:     "Movie.Title.2000.480p.WEB-DL.AAC.2.0.H.264-GRP",
						ProjectionFingerprint: fp,
						CriteriaFingerprint:   fp,
						TargetFingerprint:     fp,
						SearchFingerprint:     fp,
						PolicyFingerprint:     fp,
						EvidenceFingerprint:   fp,
						PolicyID:              "aither/duplicate/v3",
						Search:                DupeSearchEvidence{Complete: complete},
						ReviewReasons:         []DupeReason{{Code: "sd_availability", Message: "Equivalent HD retail availability requires review."}},
						Decision:              decision,
						Status:                StageStatusCompleted,
						CheckedAt:             now,
						FreshUntil:            now.Add(time.Hour),
					}},
				}
				if err := assessment.Validate(); err != nil {
					t.Fatalf("policy-only review complete=%t: %v", complete, err)
				}
				cloned, err := assessment.Clone()
				if err != nil {
					t.Fatal(err)
				}
				if len(cloned.Results[0].Matches) != 0 || cloned.Results[0].Search.Complete != complete || cloned.Results[0].Decision != decision {
					t.Fatal("review changed empty evidence, search completeness, or explicit decision")
				}
				assessment.Results[0].Status = StageStatusBlocked
				if err := assessment.Validate(); err == nil {
					t.Fatal("reviewed result without completed status was accepted")
				}
				assessment.Results[0].Status = StageStatusCompleted
				assessment.Results[0].ReviewReasons = nil
				if err := assessment.Validate(); err == nil {
					t.Fatal("review without matches or policy reasons was accepted")
				}
				assessment.Results[0].Matches = []DupeMatchProjection{{Name: "Movie.Title.2000.480p-GRP"}}
				if err := assessment.Validate(); err != nil {
					t.Fatalf("existing candidate review rejected: %v", err)
				}
				assessment.Results[0].Matches = nil
				assessment.Results[0].Decision = DupeDecisionNoMatch
				if !complete && assessment.Validate() == nil {
					t.Fatal("incomplete search was accepted as no-match")
				}
			}
		})
	}
}
