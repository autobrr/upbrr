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

func TestBuildTrackerEligibilityUsesOnlyStructuredContentFailure(t *testing.T) {
	t.Parallel()

	actual := buildTrackerEligibility(api.TrackerEligibilityInput{
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
	if !actual.Trackers[0].Eligible {
		t.Fatalf("ready-empty tracker should remain eligible: %#v", actual.Trackers[0])
	}
	if got := actual.Trackers[1].Reasons; len(got) != 1 || got[0].Code != api.TrackerEligibilityDescriptionPreparationFailed {
		t.Fatalf("structured content failure reasons = %#v", got)
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
