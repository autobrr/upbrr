// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildTrackerEligibilityCanonicalOrderingAndReasons(t *testing.T) {
	t.Parallel()

	ref := api.ReleaseRef{SourcePath: "C:\\media\\Example.Release.2026.mkv", Generation: 7}
	actual, err := buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          ref,
		SelectedTrackers: []string{" mtv ", "AITHER", "mtv", "BLU", "WARN", "AUTH"},
		Assessments: []api.TrackerEligibilityAssessment{
			{Tracker: "MTV", Duplicate: api.DupeCheckResult{Tracker: "MTV", HasDupes: true}},
			{
				Tracker:      "AITHER",
				RuleFailures: []api.RuleFailure{{Rule: "source", Disposition: api.RuleDispositionWaivable}},
			},
			{Tracker: "BLU", Duplicate: api.DupeCheckResult{Tracker: "BLU", Status: "completed"}},
			{
				Tracker:      "WARN",
				RuleFailures: []api.RuleFailure{{Rule: "advice", Disposition: api.RuleDispositionAdvisory}},
			},
			{Tracker: "AUTH", AuthRequired: true},
		},
	})
	if err != nil {
		t.Fatalf("build eligibility: %v", err)
	}
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

	actual, err := buildTrackerEligibility(api.TrackerEligibilityInput{
		Release:          api.ReleaseRef{SourcePath: "C:\\media\\Example.mkv", Generation: 1},
		SelectedTrackers: []string{},
		Assessments: []api.TrackerEligibilityAssessment{
			{Tracker: "MTV", Duplicate: api.DupeCheckResult{Status: "completed"}},
		},
	})
	if err != nil {
		t.Fatalf("build eligibility: %v", err)
	}
	if len(actual.Trackers) != 0 || len(actual.EligibleTrackers) != 0 {
		t.Fatalf("explicit empty eligibility = %#v", actual)
	}
}

func TestBuildTrackerEligibilityUsesOnlyStructuredContentFailure(t *testing.T) {
	t.Parallel()

	actual, err := buildTrackerEligibility(api.TrackerEligibilityInput{
		SelectedTrackers: []string{"ANT", "AITHER"},
		Assessments: []api.TrackerEligibilityAssessment{
			{Tracker: "ANT", Duplicate: api.DupeCheckResult{Status: "completed"}},
			{
				Tracker:   "AITHER",
				Duplicate: api.DupeCheckResult{Status: "completed"},
				ContentFailure: &api.TrackerContentFailure{
					Tracker: "AITHER",
					Code:    api.TrackerEligibilityDescriptionPreparationFailed,
					Message: "description object failed",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("build eligibility: %v", err)
	}
	if !actual.Trackers[0].Eligible {
		t.Fatalf("ready-empty tracker should remain eligible: %#v", actual.Trackers[0])
	}
	if got := actual.Trackers[1].Reasons; len(got) != 1 || got[0].Code != api.TrackerEligibilityDescriptionPreparationFailed {
		t.Fatalf("structured content failure reasons = %#v", got)
	}
}

func TestBuildTrackerEligibilityAppliesExactRuleAuthorization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		failures    []api.RuleFailure
		authorized  []string
		duplicate   bool
		ignoreDupe  bool
		wantEligible bool
		wantErr     bool
	}{
		{
			name: "waivable requires exact authorization",
			failures: []api.RuleFailure{{Rule: "container", Disposition: api.RuleDispositionWaivable}},
		},
		{
			name: "waivable exact authorization",
			failures: []api.RuleFailure{{Rule: "container", Disposition: api.RuleDispositionWaivable}},
			authorized: []string{"container"},
			wantEligible: true,
		},
		{
			name: "strict remains blocked with waivable authorization",
			failures: []api.RuleFailure{
				{Rule: "container", Disposition: api.RuleDispositionWaivable},
				{Rule: "resolution", Disposition: api.RuleDispositionStrict},
			},
			authorized: []string{"container"},
		},
		{
			name: "new rule is not covered by prior authorization",
			failures: []api.RuleFailure{
				{Rule: "container", Disposition: api.RuleDispositionWaivable},
				{Rule: "language", Disposition: api.RuleDispositionWaivable},
			},
			authorized: []string{"container"},
		},
		{
			name: "unknown authorization rejected",
			failures: []api.RuleFailure{{Rule: "container", Disposition: api.RuleDispositionWaivable}},
			authorized: []string{"language"},
			wantErr: true,
		},
		{
			name: "duplicate and rule authorization independent",
			failures: []api.RuleFailure{{Rule: "container", Disposition: api.RuleDispositionWaivable}},
			authorized: []string{"container"},
			duplicate: true,
		},
		{
			name: "duplicate ignore plus rule authorization",
			failures: []api.RuleFailure{{Rule: "container", Disposition: api.RuleDispositionWaivable}},
			authorized: []string{"container"},
			duplicate: true,
			ignoreDupe: true,
			wantEligible: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			eligibility, err := buildTrackerEligibility(api.TrackerEligibilityInput{
				SelectedTrackers: []string{"EXAMPLE"},
				Assessments: []api.TrackerEligibilityAssessment{{
					Tracker:      "EXAMPLE",
					RuleFailures: test.failures,
					Duplicate:    api.DupeCheckResult{
Tracker: "EXAMPLE",
 Status: "completed",
 HasDupes: test.duplicate,
},
					Choices: api.TrackerReviewChoices{
						IgnoreDuplicate:        test.ignoreDupe,
						AuthorizedRuleFailures: test.authorized,
					},
				}},
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v, wantErr=%t", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if len(eligibility.Trackers) != 1 || eligibility.Trackers[0].Eligible != test.wantEligible {
				t.Fatalf("eligibility = %#v", eligibility)
			}
			if len(test.failures) > 0 && len(eligibility.Trackers[0].RuleDecisions) != len(test.failures) {
				t.Fatalf("rule evidence was dropped: %#v", eligibility.Trackers[0].RuleDecisions)
			}
		})
	}
}

func TestLogTrackerEligibilityUsesCanonicalReasonsAndContextLogger(t *testing.T) {
	t.Parallel()

	fallback := &eligibilityCaptureLogger{}
	scoped := &eligibilityCaptureLogger{}
	ctx := logging.WithOperationLogger(context.Background(), scoped)
	localPath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
	eligibility := api.TrackerEligibility{
		Trackers: []api.TrackerEligibilityState{
			{
				Tracker: "ANT",
				Reasons: []api.TrackerEligibilityReason{
					{Code: api.TrackerEligibilityDuplicate, Message: "A blocking duplicate was found."},
					{Code: api.TrackerEligibilityAuthRequired, Message: "Tracker authentication is required."},
				},
			},
			{
				Tracker: "AITHER",
				Reasons: []api.TrackerEligibilityReason{{
					Code:    api.TrackerEligibilityDescriptionPreparationFailed,
					Message: "api_key=secret-value source=" + localPath,
				}},
			},
			{Tracker: "BTN", Eligible: true},
		},
		EligibleTrackers: []string{"BTN"},
	}

	logTrackerEligibility(ctx, fallback, "dry_run", eligibility)
	if fallback.count() != 0 {
		t.Fatalf("fallback logger received operation lines: %#v", fallback.all())
	}
	info := scoped.level("info")
	if len(info) != 3 {
		t.Fatalf("info lines = %#v", info)
	}
	wantCounts := "reasons=auth_required:1,description_preparation_failed:1,duplicate:1"
	if !strings.Contains(info[0], wantCounts) {
		t.Fatalf("summary = %q, want %q", info[0], wantCounts)
	}
	if !strings.Contains(info[1], "tracker=ANT") || !strings.Contains(info[1], "reasons=auth_required,duplicate") {
		t.Fatalf("ANT blocker line = %q", info[1])
	}
	debug := strings.Join(scoped.level("debug"), "\n")
	if strings.Contains(debug, "secret-value") || strings.Contains(debug, localPath) {
		t.Fatalf("debug details were not sanitized: %q", debug)
	}
	trace := scoped.level("trace")
	if len(trace) != 1 || !strings.Contains(trace[0], "tracker=BTN eligible=true") {
		t.Fatalf("eligible trace = %#v", trace)
	}
}

func TestPersistTrackerRuleDecisionsStoresAuthorizationAndClearsResolvedTrackers(t *testing.T) {
	t.Parallel()

	repo := &recordingRuleDecisionRepo{}
	err := persistTrackerRuleDecisions(context.Background(), repo, `C:\media\Example.Release.2026.mkv`, api.TrackerEligibility{
		Trackers: []api.TrackerEligibilityState{
			{
				Tracker: "HDS",
				RuleDecisions: []api.RuleDecision{
					{
Rule: "min_resolution",
 Reason: "requires 720p",
 Disposition: api.RuleDispositionStrict,
},
					{
Rule: "imdb_required",
 Reason: "requires IMDb",
 Disposition: api.RuleDispositionWaivable,
 Authorized: true,
},
				},
			},
			{Tracker: "BLU"},
		},
	})
	if err != nil {
		t.Fatalf("persist rule decisions: %v", err)
	}
	if len(repo.calls) != 2 {
		t.Fatalf("save calls = %#v", repo.calls)
	}
	if repo.calls[1].tracker != "BLU" || len(repo.calls[1].failures) != 0 {
		t.Fatalf("resolved tracker was not cleared: %#v", repo.calls[1])
	}
	stored := repo.calls[0].failures
	if len(stored) != 2 || stored[0].Disposition != api.RuleDispositionStrict || stored[0].Authorized {
		t.Fatalf("strict decision = %#v", stored)
	}
	if stored[1].Disposition != api.RuleDispositionWaivable || !stored[1].Authorized {
		t.Fatalf("authorized decision = %#v", stored[1])
	}
}

type ruleDecisionSaveCall struct {
	path     string
	tracker  string
	failures []api.TrackerRuleFailure
}

type recordingRuleDecisionRepo struct {
	api.TrackerStateRepository
	calls []ruleDecisionSaveCall
}

func (r *recordingRuleDecisionRepo) SaveTrackerRuleFailures(
	_ context.Context,
	path string,
	tracker string,
	failures []api.TrackerRuleFailure,
) error {
	r.calls = append(r.calls, ruleDecisionSaveCall{
		path:     path,
		tracker:  tracker,
		failures: append([]api.TrackerRuleFailure(nil), failures...),
	})
	return nil
}

type eligibilityLogEntry struct {
	level   string
	message string
}

type eligibilityCaptureLogger struct {
	mu      sync.Mutex
	entries []eligibilityLogEntry
}

func (l *eligibilityCaptureLogger) record(level string, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, eligibilityLogEntry{level: level, message: fmt.Sprintf(format, args...)})
}

func (l *eligibilityCaptureLogger) Tracef(format string, args ...any) {
	l.record("trace", format, args...)
}

func (l *eligibilityCaptureLogger) Debugf(format string, args ...any) {
	l.record("debug", format, args...)
}

func (l *eligibilityCaptureLogger) Infof(format string, args ...any) {
	l.record("info", format, args...)
}

func (l *eligibilityCaptureLogger) Warnf(format string, args ...any) {
	l.record("warn", format, args...)
}

func (l *eligibilityCaptureLogger) Errorf(format string, args ...any) {
	l.record("error", format, args...)
}

func (l *eligibilityCaptureLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *eligibilityCaptureLogger) all() []eligibilityLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]eligibilityLogEntry(nil), l.entries...)
}

func (l *eligibilityCaptureLogger) level(level string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := make([]string, 0)
	for _, entry := range l.entries {
		if entry.level == level {
			entries = append(entries, entry.message)
		}
	}
	return entries
}
