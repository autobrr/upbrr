// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestMergeReviewedReleaseNameProjectionsReplacesGeneratedNamingNotices(t *testing.T) {
	t.Parallel()

	current := testProjectionSet(t)
	previous := &current.Projections[0]
	previous.UploadReady = false
	previous.PolicyDecisions = []api.TrackerPolicyDecision{
		{Code: releaseNameConfirmationDecisionCode, Decision: "confirmation_required"},
		{
			Code:     releaseNameStructureDecisionCode,
			Decision: "old-structure",
			Message:  "obsolete structure provenance",
		},
		{
			Code:         "release_name_override",
			Decision:     "rebuilt",
			Message:      "obsolete naming explanation",
			NamingRole:   "title",
			NamingRuleID: "required-title-v1",
		},
		{
			Code:     "unrelated_policy",
			Decision: "preserved",
			Message:  "keep this decision",
		},
	}
	previous.RequiredActions = []api.RequiredAction{{
		ID:        "confirm-name",
		Kind:      api.RequiredActionProvideTrackerInput,
		TrackerID: previous.TrackerID,
		Status:    api.RequiredActionStatusPending,
	}}

	rebuilt := current
	rebuilt.Projections = append([]api.TrackerReleaseProjection(nil), current.Projections...)
	next := &rebuilt.Projections[0]
	next.UploadReady = true
	next.PolicyDecisions = []api.TrackerPolicyDecision{
		{Code: releaseNameConfirmationDecisionCode, Decision: "confirmed"},
		{
			Code:     releaseNameStructureDecisionCode,
			Decision: "current-structure",
			Message:  "current structure provenance",
		},
		{
			Code:         "release_name_override",
			Decision:     "rebuilt",
			Message:      "current naming explanation",
			NamingRole:   "title",
			NamingRuleID: "required-title-v2",
		},
		{
			Code:         "release_name_override",
			Decision:     "rebuilt",
			Message:      "same text, distinct rule",
			NamingRole:   "title",
			NamingRuleID: "required-title-v3",
		},
	}
	next.RequiredActions = nil

	merged, err := mergeReviewedReleaseNameProjections(current, rebuilt, previous.TrackerID, true)
	if err != nil {
		t.Fatalf("merge reviewed release-name projections: %v", err)
	}
	decisions := merged.Projections[0].PolicyDecisions
	if slices.ContainsFunc(decisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Message == "obsolete naming explanation" || decision.Message == "obsolete structure provenance"
	}) || !slices.ContainsFunc(decisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Message == "current naming explanation" && decision.NamingRuleID == "required-title-v2"
	}) || !slices.ContainsFunc(decisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == releaseNameStructureDecisionCode && decision.Message == "current structure provenance"
	}) || !slices.ContainsFunc(decisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Message == "same text, distinct rule" && decision.NamingRuleID == "required-title-v3"
	}) || !slices.ContainsFunc(decisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == "unrelated_policy" && decision.Message == "keep this decision"
	}) {
		t.Fatalf("merged naming decisions = %#v", decisions)
	}
}

func TestConfirmedNameProjectionAuthorityIsServerOwned(t *testing.T) {
	t.Parallel()

	fingerprint := testFingerprint(t, "confirmed-name-authority")
	state := State{
		Workflow: api.ReleaseWorkflow{ProjectionInstructions: &api.TrackerProjectionInstructionSnapshotRef{ID: "instructions-1", Revision: 2}},
		ProjectionInstructions: map[api.TrackerProjectionInstructionSnapshotID]api.TrackerProjectionInstructionSnapshot{
			"instructions-1": {
				Revision: 2,
				Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{
					"ALPHA": {ConfirmedNameFingerprint: fingerprint},
				},
			},
		},
	}
	matching := map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {ConfirmedNameFingerprint: fingerprint}}
	if err := validateConfirmedNameProjectionInstructions(&state, matching); err != nil {
		t.Fatalf("accept retained confirmation marker: %v", err)
	}
	retained, err := retainConfirmedNameProjectionInstructions(&state, []api.TrackerID{"ALPHA"}, nil)
	if err != nil || retained["ALPHA"].ConfirmedNameFingerprint != fingerprint {
		t.Fatalf("retain server confirmation marker = %#v, %v", retained, err)
	}
	forged := map[api.TrackerID]api.TrackerProjectionInstructions{"ALPHA": {ConfirmedNameFingerprint: testFingerprint(t, "forged")}}
	if err := validateConfirmedNameProjectionInstructions(&state, forged); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("forged confirmation marker error = %v", err)
	}

	module, _ := newTestModule(t, testPreparer())
	factInstructions := api.ReleaseFactInstructions{SourceLookup: "Example Release"}
	_, err = module.Continue(context.Background(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: "forged-confirmed-name",
		Goal:           api.WorkflowGoalTrackersAssessed,
		Intent: api.WorkflowIntent{
			FactInstructions: &factInstructions,
			ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{
				"ALPHA": {ConfirmedNameFingerprint: fingerprint},
			},
		},
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("continue accepts forged confirmation marker: %v", err)
	}
}
