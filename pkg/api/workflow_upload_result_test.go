// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRetainedSubmissionRetryEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                string
		submission          StageStatus
		client              StageStatus
		code                OperationFailureCode
		clientCode          OperationFailureCode
		wantSubmissionRetry bool
		wantClientRetry     bool
		wantStatus          StageStatus
	}{
		{"rejected", StageStatusFailed, StageStatusPending, OperationFailureInternal, "", true, false, StageStatusFailed},
		{"rejection receipt pending", StageStatusFailed, StageStatusPending, "receipt_retention", "", false, false, StageStatusFailed},
		{"accepted receipt pending", StageStatusCompleted, StageStatusUnavailable, "receipt_retention", "", false, false, StageStatusPartial},
		{"accepted receipt pending with no seed", StageStatusCompleted, StageStatusSkipped, "receipt_retention", "", false, false, StageStatusPartial},
		{"unknown", StageStatusUnavailable, StageStatusPending, OperationFailureUnknownOutcome, "", false, false, StageStatusFailed},
		{"client failure after accepted submission", StageStatusCompleted, StageStatusFailed, OperationFailureClientInjection, OperationFailureClientInjection, false, true, StageStatusPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := UploadTrackerResult{
				TrackerID:             "EXAMPLE",
				SubmissionStatus:      test.submission,
				ClientInjectionStatus: test.client,
				ClientFailureCode:     test.clientCode,
				Failures: []WorkflowFailure{{Failure: OperationFailure{
					Code:      test.code,
					Operation: OperationKindUploadExecute,
					Message:   "Synthetic result",
					Recovery:  OperationRecoveryNone,
				}, Resource: "effect-example"}},
			}
			payload, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var retained UploadTrackerResult
			if err := json.Unmarshal(payload, &retained); err != nil {
				t.Fatal(err)
			}
			if retained.SubmissionRetryable() != test.wantSubmissionRetry || retained.ClientInjectionRetryable() != test.wantClientRetry || retained.DerivedStatus() != test.wantStatus {
				t.Fatalf("submission retry=%v client retry=%v status=%s", retained.SubmissionRetryable(), retained.ClientInjectionRetryable(), retained.DerivedStatus())
			}
			retained.Status = retained.DerivedStatus()
			retained.SubmissionReceiptID, retained.SubmissionOperationID = "effect-example", "operation-example"
			if test.clientCode == OperationFailureClientInjection {
				retained.Failures[0].Failure.Operation = OperationKindClientInjection
				retained.ClientInjectionMessage = "Synthetic client failure"
			}
			snapshot := UploadResult{
				ID:               "result",
				WorkflowID:       "workflow",
				Revision:         1,
				CreatedAt:        time.Now(),
				ProjectionSet:    TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1},
				Dupes:            DupeAssessmentRef{ID: "dupes", Revision: 1},
				Media:            MediaArtifactSetRef{ID: "media", Revision: 1},
				Descriptions:     DescriptionSetRef{ID: "descriptions", Revision: 1},
				InputFingerprint: WorkflowFingerprint(strings.Repeat("a", 64)),
				Results:          []UploadTrackerResult{retained},
				Status:           retained.Status,
			}
			if err := snapshot.Validate(); err != nil {
				t.Fatalf("retained snapshot invalid: %v", err)
			}
			if test.code == OperationFailureReceiptRetention && test.submission == StageStatusCompleted {
				snapshot.Results[0].SubmissionReceiptID = ""
				if err := snapshot.Validate(); err == nil {
					t.Fatal("accepted receipt failure without exact binding was accepted")
				}
			}
		})
	}
}
