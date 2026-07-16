// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildTrackerEligibilityCanonicalOrderingAndReasons(t *testing.T) {
	t.Parallel()

	ref := api.ReleaseRef{SourcePath: "C:\\media\\Example.Release.2026.mkv", Generation: 7}
	actual := buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          ref,
		SelectedTrackers: []string{" mtv ", "AITHER", "mtv", "BLU", "WARN", "AUTH"},
		Assessments: []api.TrackerEligibilityAssessment{
			{Tracker: "MTV", Duplicate: api.DupeCheckResult{Tracker: "MTV", HasDupes: true}},
			{
				Tracker:      "AITHER",
				RuleFailures: []api.RuleFailure{{Rule: "source", Severity: api.RuleFailureSeverityBlocking}},
			},
			{Tracker: "BLU", Duplicate: api.DupeCheckResult{Tracker: "BLU", Status: "completed"}},
			{
				Tracker:      "WARN",
				RuleFailures: []api.RuleFailure{{Rule: "advice", Severity: api.RuleFailureSeverityWarning}},
			},
			{Tracker: "AUTH", AuthRequired: true},
		},
	})
	if actual.Release != ref {
		t.Fatalf("release = %#v", actual.Release)
	}
	if got, want := actual.EligibleTrackers, []string{"BLU", "WARN"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("eligible = %v, want %v", got, want)
	}
	wantCodes := []api.TrackerEligibilityReasonCode{
		api.TrackerEligibilityDuplicate,
		api.TrackerEligibilityBlockingRule,
	}
	for index, code := range wantCodes {
		if got := actual.Trackers[index].Reasons[0].Code; got != code {
			t.Fatalf("tracker[%d] reason = %q, want %q", index, got, code)
		}
	}
	if got := actual.Trackers[4].Reasons[0].Code; got != api.TrackerEligibilityAuthRequired {
		t.Fatalf("auth reason = %q", got)
	}
}

func TestBuildTrackerEligibilityExplicitEmptyStaysEmpty(t *testing.T) {
	t.Parallel()

	actual := buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          api.ReleaseRef{SourcePath: "C:\\media\\Example.mkv", Generation: 1},
		SelectedTrackers: []string{},
		Assessments: []api.TrackerEligibilityAssessment{
			{Tracker: "MTV", Duplicate: api.DupeCheckResult{Status: "completed"}},
		},
	})
	if len(actual.Trackers) != 0 || len(actual.EligibleTrackers) != 0 {
		t.Fatalf("explicit empty eligibility = %#v", actual)
	}
}
