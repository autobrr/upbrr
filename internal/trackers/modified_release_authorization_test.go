// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestModifiedReleaseAcknowledgementDoesNotAuthorizeTrackerWarnings(t *testing.T) {
	projection := api.TrackerReleaseProjection{TrackerID: "ALPHA", ModifiedReleaseDecision: new(true)}
	failures := []api.RuleFailure{
		{
Rule: "modified_release",
 Reason: "source provenance requires review",
 Disposition: api.RuleDispositionWaivable,
},
		{
Rule: "language",
 Reason: "language requires review",
 Disposition: api.RuleDispositionWaivable,
},
	}
	if err := ApplyProjectionRuleFailures(&projection, failures, api.WorkflowExecutionModeNormal, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, decision := range projection.PolicyDecisions {
		if decision.Code == "modified_release" && decision.Blocking {
			t.Fatal("release answer was not applied")
		}
		if decision.Code == "language" && !decision.Blocking {
			t.Fatal("release answer authorized unrelated tracker warning")
		}
	}
	if failure, err := FirstBlockingRuleFailure("ALPHA", failures, api.WorkflowExecutionModeNormal, &projection); err != nil || failure == nil || failure.Rule != "language" {
		t.Fatalf("submission gate: failure=%#v err=%v", failure, err)
	}

	projection.ModifiedReleaseDecision = new(false)
	if err := ApplyProjectionRuleFailures(&projection, failures, api.WorkflowExecutionModeNormal, "", nil); err != nil {
		t.Fatal(err)
	}
	if projection.Readiness != api.ReadinessStatusIneligible || len(projection.RequiredActions) != 0 {
		t.Fatalf("declined release still requests tracker input: readiness=%s actions=%#v", projection.Readiness, projection.RequiredActions)
	}
	if failure, err := FirstBlockingRuleFailure("ALPHA", failures, api.WorkflowExecutionModeNormal, &projection); err != nil || failure == nil || failure.Rule != "modified_release" {
		t.Fatalf("decline did not block submission: failure=%#v err=%v", failure, err)
	}
	projection.ModifiedReleaseDecision = new(true)
	failures[0].Disposition = api.RuleDispositionStrict
	if err := ApplyProjectionRuleFailures(&projection, failures[:1], api.WorkflowExecutionModeNormal, "", nil); err != nil {
		t.Fatal(err)
	}
	if !projection.PolicyDecisions[0].Blocking {
		t.Fatal("release answer waived authoritative scene rename")
	}
}
