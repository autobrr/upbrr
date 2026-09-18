// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func laneEligibilityTestCurrent() CommandResult {
	current := continuationTestCurrent()
	for index := range current.Projections.Projections {
		current.Projections.Projections[index].UploadReady = true
	}
	current.Dupes = &api.DupeAssessment{
		Results: []api.TrackerDupeAssessment{
			{
TrackerID: "ALPHA",
 Decision: api.DupeDecisionNoMatch,
 Status: api.StageStatusCompleted,
},
			{
TrackerID: "BETA",
 Decision: api.DupeDecisionNoMatch,
 Status: api.StageStatusCompleted,
},
		},
		Status: api.StageStatusCompleted,
	}
	return current
}

func laneOutcome(t *testing.T, current CommandResult, trackerID api.TrackerID) api.TrackerLaneOutcome {
	t.Helper()

	for _, lane := range projectWorkflowContinuation(current).TrackerOutcomes {
		if lane.TrackerID == trackerID {
			return lane
		}
	}
	t.Fatalf("tracker lane %s is missing", trackerID)
	return api.TrackerLaneOutcome{}
}

func assertLaneEligibility(
	t *testing.T,
	lane api.TrackerLaneOutcome,
	eligibility api.UploadEligibility,
	reason api.UploadSkipReason,
) {
	t.Helper()

	if lane.UploadEligibility != eligibility || lane.UploadSkipReason != reason {
		t.Fatalf(
			"tracker=%s eligibility=%s reason=%s want eligibility=%s reason=%s",
			lane.TrackerID,
			lane.UploadEligibility,
			lane.UploadSkipReason,
			eligibility,
			reason,
		)
	}
}

func TestLaneUploadEligibilityReportsBlockingDuplicateWithoutFailingSiblings(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Dupes.Results[1].Decision = api.DupeDecisionAccepted
	assertLaneEligibility(t, laneOutcome(t, current, "ALPHA"), api.UploadEligibilityEligible, "")
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonDuplicateFound,
	)
}

func TestLaneUploadEligibilityReportsIgnoredDuplicateAsEligible(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Dupes.Results[1].Decision = api.DupeDecisionIgnored
	assertLaneEligibility(t, laneOutcome(t, current, "BETA"), api.UploadEligibilityEligible, "")
}

func TestLaneUploadEligibilityIsUnknownBeforeDuplicateDecision(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Dupes.Results[1].Decision = api.DupeDecisionPending
	assertLaneEligibility(t, laneOutcome(t, current, "BETA"), api.UploadEligibilityUnknown, "")

	pendingDupes := laneEligibilityTestCurrent()
	pendingDupes.Dupes = nil
	assertLaneEligibility(t, laneOutcome(t, pendingDupes, "BETA"), api.UploadEligibilityUnknown, "")
}

func TestLaneUploadEligibilityReportsFailedDuplicateSearch(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Dupes.Results[1].Status = api.StageStatusFailed
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonDuplicateCheckFailed,
	)
}

func TestLaneUploadEligibilityReportsProjectionNotUploadReady(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Projections.Projections[1].UploadReady = false
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonNotReady,
	)
}

func TestLaneUploadEligibilityReportsImageHostFailure(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Media = &api.MediaArtifactSet{
		Status: api.StageStatusCompleted,
		Failures: []api.WorkflowFailure{{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureImageHostUnavailable,
				Operation: api.OperationKindImageHosting,
				Message:   "Required image host failed.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "BETA",
			Resource:  "pixhost",
		}},
	}
	assertLaneEligibility(t, laneOutcome(t, current, "ALPHA"), api.UploadEligibilityEligible, "")
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonImageHostingFailed,
	)
}

func TestLaneUploadEligibilityReportsSkippedDescription(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Projections.Projections[1].Artifacts.Description = true
	current.Descriptions = &api.DescriptionSet{
		Status: api.StageStatusCompleted,
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "BETA", Status: api.StageStatusSkipped},
		},
	}
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonDescriptionSkipped,
	)
}

func TestLaneUploadEligibilityReportsTrackerLeftOutOfApprovalGate(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.TrackerApproval = &api.TrackerApprovalSnapshot{
		CandidateTrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
		ApprovedTrackerIDs:  []api.TrackerID{"ALPHA"},
	}
	assertLaneEligibility(t, laneOutcome(t, current, "ALPHA"), api.UploadEligibilityEligible, "")
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonTrackerNotApprovedInGate,
	)
}

// The rendered lane set and the resolved downstream set read the same retained
// evidence, so an eligible lane must always match the downstream projection.
func TestLaneUploadEligibilityMatchesDownstreamEligibleProjections(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Dupes.Results[1].Decision = api.DupeDecisionAccepted
	eligible := DownstreamEligibleProjections(*current.Projections, *current.Dupes)
	downstream := make(map[api.TrackerID]struct{}, len(eligible.Projections))
	for _, projection := range eligible.Projections {
		downstream[projection.TrackerID] = struct{}{}
	}
	for _, lane := range projectWorkflowContinuation(current).TrackerOutcomes {
		_, included := downstream[lane.TrackerID]
		if included != (lane.UploadEligibility == api.UploadEligibilityEligible) {
			t.Fatalf(
				"tracker=%s downstream=%t eligibility=%s",
				lane.TrackerID,
				included,
				lane.UploadEligibility,
			)
		}
	}
}
