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
				Decision:  api.DupeDecisionNoMatch,
				Status:    api.StageStatusCompleted,
			},
			{
				TrackerID: "BETA",
				Decision:  api.DupeDecisionNoMatch,
				Status:    api.StageStatusCompleted,
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

func TestLaneUploadEligibilityReportsFailedDescription(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Projections.Projections[1].Artifacts.Description = true
	current.Descriptions = &api.DescriptionSet{
		Status: api.StageStatusPartial,
		TrackerResults: []api.DescriptionTrackerResult{
			{TrackerID: "BETA", Status: api.StageStatusFailed},
		},
	}
	assertLaneEligibility(t, laneOutcome(t, current, "ALPHA"), api.UploadEligibilityEligible, "")
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonDescriptionFailed,
	)
}

// The upload plan blocks a tracker whose description outcome never arrived, so
// the lane must not advertise an upload for it either.
func TestLaneUploadEligibilityReportsMissingDescriptionOutcome(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Projections.Projections[1].Artifacts.Description = true
	current.Descriptions = &api.DescriptionSet{Status: api.StageStatusPartial}
	assertLaneEligibility(
		t,
		laneOutcome(t, current, "BETA"),
		api.UploadEligibilitySkipped,
		api.UploadSkipReasonDescriptionFailed,
	)
}

// uploadDryRunReports only ever emits completed, skipped, or failed, so those
// are the statuses the lane has to classify.
func TestLaneUploadEligibilityReportsDryRunOutcomes(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		status      api.StageStatus
		eligibility api.UploadEligibility
		reason      api.UploadSkipReason
	}{
		{
			name:        "completed",
			status:      api.StageStatusCompleted,
			eligibility: api.UploadEligibilityEligible,
			reason:      "",
		},
		{
			name:        "skipped",
			status:      api.StageStatusSkipped,
			eligibility: api.UploadEligibilitySkipped,
			reason:      api.UploadSkipReasonUploadPreparationSkipped,
		},
		{
			name:        "failed",
			status:      api.StageStatusFailed,
			eligibility: api.UploadEligibilitySkipped,
			reason:      api.UploadSkipReasonUploadPreparationFailed,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			current := laneEligibilityTestCurrent()
			current.DryRun = &api.UploadDryRunResult{
				Status: api.StageStatusPartial,
				Reports: []api.TrackerDryRunReport{
					{TrackerID: "ALPHA", Status: api.StageStatusCompleted},
					{TrackerID: "BETA", Status: testCase.status},
				},
			}
			assertLaneEligibility(t, laneOutcome(t, current, "ALPHA"), api.UploadEligibilityEligible, "")
			assertLaneEligibility(t, laneOutcome(t, current, "BETA"), testCase.eligibility, testCase.reason)
		})
	}
}

// A dry run can target a subset of trackers, so an absent report is not
// evidence that the tracker was excluded.
func TestLaneUploadEligibilityIsUnknownWhenDryRunOmitsTracker(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.DryRun = &api.UploadDryRunResult{
		Status:  api.StageStatusCompleted,
		Reports: []api.TrackerDryRunReport{{TrackerID: "ALPHA", Status: api.StageStatusCompleted}},
	}
	assertLaneEligibility(t, laneOutcome(t, current, "ALPHA"), api.UploadEligibilityEligible, "")
	assertLaneEligibility(t, laneOutcome(t, current, "BETA"), api.UploadEligibilityUnknown, "")
}

// A rule-gated projection reports itself as not upload ready, so the lane has
// to carry the rule message or the page can only say "not ready".
func TestLaneUploadEligibilityCarriesBlockingRuleMessage(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		disposition api.RuleDisposition
	}{
		{name: "waivable", disposition: api.RuleDispositionWaivable},
		{name: "strict", disposition: api.RuleDispositionStrict},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			current := laneEligibilityTestCurrent()
			blocked := &current.Projections.Projections[1]
			blocked.UploadReady = false
			blocked.Readiness = api.ReadinessStatusBlocked
			blocked.PolicyDecisions = []api.TrackerPolicyDecision{
				{
Code: "genre_not_accepted",
 Decision: "ineligible",
 Blocking: false,
 Message: "advisory only",
},
				{
					Code:        "genre_not_accepted",
					Decision:    "ineligible",
					Blocking:    true,
					Message:     "Tracker does not accept this genre.",
					Disposition: testCase.disposition,
				},
			}
			lane := laneOutcome(t, current, "BETA")
			assertLaneEligibility(t, lane, api.UploadEligibilitySkipped, api.UploadSkipReasonNotReady)
			if lane.UploadSkipDetail != "Tracker does not accept this genre." {
				t.Fatalf("tracker=BETA detail=%q want the blocking rule message", lane.UploadSkipDetail)
			}
			if sibling := laneOutcome(t, current, "ALPHA"); sibling.UploadSkipDetail != "" {
				t.Fatalf("tracker=ALPHA detail=%q want empty", sibling.UploadSkipDetail)
			}
		})
	}
}

// Readiness failures with no rule evidence keep the generic label.
func TestLaneUploadEligibilityOmitsDetailWithoutBlockingRule(t *testing.T) {
	t.Parallel()

	current := laneEligibilityTestCurrent()
	current.Projections.Projections[1].UploadReady = false
	lane := laneOutcome(t, current, "BETA")
	assertLaneEligibility(t, lane, api.UploadEligibilitySkipped, api.UploadSkipReasonNotReady)
	if lane.UploadSkipDetail != "" {
		t.Fatalf("tracker=BETA detail=%q want empty", lane.UploadSkipDetail)
	}
}
