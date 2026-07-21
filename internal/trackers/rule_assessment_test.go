// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAssessRuleFailuresRetainsMixedEvidenceAndExactAuthorization(t *testing.T) {
	t.Parallel()
	failures := []api.RuleFailure{
		{
Rule: "advice",
 Reason: "recommended",
 Disposition: api.RuleDispositionAdvisory,
},
		{
Rule: "container",
 Reason: "container rejected",
 Disposition: api.RuleDispositionWaivable,
},
		{
Rule: "resolution",
 Reason: "resolution rejected",
 Disposition: api.RuleDispositionStrict,
},
	}
	assessment, err := AssessRuleFailures("example", failures, []string{"container"})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if assessment.Eligible {
		t.Fatal("strict failure became eligible after waivable authorization")
	}
	if len(assessment.Failures) != 3 || len(assessment.Advisory) != 1 || len(assessment.Waivable) != 1 || len(assessment.Strict) != 1 || len(assessment.Authorized) != 1 {
		t.Fatalf("mixed assessment = %#v", assessment)
	}
	if !assessment.Decisions[1].Authorized || assessment.Decisions[2].Authorized {
		t.Fatalf("authorization projection = %#v", assessment.Decisions)
	}

	onlyWaivable, err := AssessRuleFailures("EXAMPLE", failures[1:2], []string{"container"})
	if err != nil || !onlyWaivable.Eligible || len(onlyWaivable.Failures) != 1 {
		t.Fatalf("authorized waivable assessment = %#v, %v", onlyWaivable, err)
	}
}

func TestAssessRuleFailuresRejectsUnknownOrNonWaivableAuthorization(t *testing.T) {
	t.Parallel()
	failures := []api.RuleFailure{
		{Rule: "advice", Disposition: api.RuleDispositionAdvisory},
		{Rule: "strict", Disposition: api.RuleDispositionStrict},
		{Rule: "waivable", Disposition: api.RuleDispositionWaivable},
	}
	for _, rule := range []string{"missing", "advice", "strict"} {
		if _, err := AssessRuleFailures("EXAMPLE", failures, []string{rule}); err == nil {
			t.Fatalf("authorization %q succeeded", rule)
		}
	}
}

func TestValidateRuleAuthorizationsIsTrackerScopedAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	failures := map[string][]api.RuleFailure{
		"A": {{Rule: "container", Disposition: api.RuleDispositionWaivable}},
		"B": {{Rule: "language", Disposition: api.RuleDispositionWaivable}},
	}
	valid := []api.RuleAuthorization{{Tracker: " a ", Rules: []string{"container"}}}
	if err := ValidateRuleAuthorizations([]string{"A", "B"}, failures, valid); err != nil {
		t.Fatalf("valid authorization: %v", err)
	}
	rules, err := AuthorizedRulesForTracker(valid, "A")
	if err != nil || !reflect.DeepEqual(rules, []string{"container"}) {
		t.Fatalf("authorized rules = %v, %v", rules, err)
	}
	if err := ValidateRuleAuthorizations([]string{"A", "B"}, failures, []api.RuleAuthorization{{Tracker: "A", Rules: []string{"language"}}}); err == nil {
		t.Fatal("cross-tracker rule authorization succeeded")
	}
	if err := ValidateRuleAuthorizations([]string{"A"}, failures, []api.RuleAuthorization{{Tracker: "A"}, {Tracker: "a"}}); err == nil {
		t.Fatal("duplicate tracker authorization records succeeded")
	}
	if err := ValidateRuleAuthorizations([]string{"A"}, failures, []api.RuleAuthorization{{Tracker: "B", Rules: []string{"language"}}}); err == nil {
		t.Fatal("unselected tracker authorization succeeded")
	}
}

func TestAssessRuleFailuresNormalizesLegacyAndUnknownDisposition(t *testing.T) {
	t.Parallel()
	assessment, err := AssessRuleFailures("EXAMPLE", []api.RuleFailure{
		{Rule: "legacy_warning", Disposition: "warning"},
		{Rule: "legacy_blocking", Disposition: "blocking"},
		{Rule: "unknown", Disposition: "new_value"},
	}, nil)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	want := []api.RuleDisposition{api.RuleDispositionAdvisory, api.RuleDispositionWaivable, api.RuleDispositionStrict}
	for idx, failure := range assessment.Failures {
		if failure.Disposition != want[idx] {
			t.Fatalf("failure[%d] disposition=%q, want %q", idx, failure.Disposition, want[idx])
		}
	}
}
