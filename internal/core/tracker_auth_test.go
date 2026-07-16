// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"reflect"
	"slices"
	"testing"

	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type trackerAuthPreflightTestService struct {
	capabilities []api.TrackerAuthCapability
	statuses     []api.TrackerAuthStatus
	validated    []string
}

func (s *trackerAuthPreflightTestService) Capabilities(context.Context) ([]api.TrackerAuthCapability, error) {
	return append([]api.TrackerAuthCapability(nil), s.capabilities...), nil
}

func (s *trackerAuthPreflightTestService) ValidateMany(_ context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	s.validated = append([]string(nil), trackerIDs...)
	return append([]api.TrackerAuthStatus(nil), s.statuses...), nil
}

type trackerAuthPreflightDupeService struct {
	trackers []string
}

func checkWithAuthForTest(
	ctx context.Context,
	module *dupeModule,
	interaction api.InteractionMode,
	subject api.DuplicateSubject,
	trackerIDs []string,
) (api.DupeCheckSummary, error) {
	ready := trackerIDs
	var blocked []api.DupeCheckResult
	var err error
	if interaction == api.InteractionModeInteractive {
		ready, blocked, err = module.preflightGUITrackerAuth(ctx, subject, trackerIDs)
		if err != nil {
			return api.DupeCheckSummary{}, err
		}
	}
	summary, _, err := checkDuplicateAssessment(ctx, module.services.Dupes, subject, ready, dupechecking.CheckOptions{})
	if err != nil {
		return summary, err
	}
	summary.Results = append(summary.Results, blocked...)
	return summary, nil
}

func (s *trackerAuthPreflightDupeService) Check(_ context.Context, meta api.DuplicateSubject, trackerIDs []string) (api.DupeCheckSummary, error) {
	s.trackers = append([]string(nil), trackerIDs...)
	results := make([]api.DupeCheckResult, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		results = append(results, api.DupeCheckResult{Tracker: trackerID, Status: "completed"})
	}
	return api.DupeCheckSummary{SourcePath: meta.SourcePath, Results: results}, nil
}

func (s *trackerAuthPreflightDupeService) CheckWithAssessment(
	ctx context.Context,
	meta api.DuplicateSubject,
	trackerIDs []string,
	_ dupechecking.CheckOptions,
) (api.DupeCheckSummary, dupechecking.Assessment, error) {
	summary, err := s.Check(ctx, meta, trackerIDs)
	return summary, dupechecking.EmptyAssessment(), err
}

func TestCheckGUIDupesWithAuthSkipsBlockedTrackerAndContinuesReadyTrackers(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{
			TrackerID:         "PTP",
			SupportsLogin:     true,
			SupportsManual2FA: true,
		}},
		statuses: []api.TrackerAuthStatus{{
			TrackerID: "PTP",
			State:     trackerauth.StateLoginRequired,
			Needs2FA:  true,
			Message:   "manual 2FA required",
		}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := newTestCore(testCoreOptions{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Dupes:       dupes,
			TrackerAuth: auth,
		},
	})
	var progress []api.DupeProgressUpdate
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		progress = append(progress, update)
	})

	summary, err := checkWithAuthForTest(ctx, coreSvc.dupe, api.InteractionModeInteractive, api.DuplicateSubject{SourcePath: "source.mkv"}, []string{"PTP", "AITHER"})
	if err != nil {
		t.Fatalf("check WebUI dupes with auth: %v", err)
	}
	if !slices.Equal(auth.validated, []string{"PTP"}) {
		t.Fatalf("expected managed auth validation for PTP, got %v", auth.validated)
	}
	if !slices.Equal(dupes.trackers, []string{"AITHER"}) {
		t.Fatalf("expected only auth-ready trackers to reach dupe checking, got %v", dupes.trackers)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected ready and auth-blocked results, got %#v", summary.Results)
	}
	blocked := summary.Results[1]
	expectedReason := "tracker auth not ready: manual 2FA required; configure tracker auth and retry"
	if blocked.Tracker != "PTP" || !blocked.Skipped || blocked.SkipCode != dupeSkipCodeTrackerAuthNotReady ||
		blocked.SkipReason != expectedReason || blocked.Status != "skipped" || blocked.CheckedAt.IsZero() {
		t.Fatalf("expected PTP auth-required skip, got %#v", blocked)
	}
	if len(progress) != 2 || progress[0].Status != "running" || progress[1].Status != "skipped" {
		t.Fatalf("expected running then skipped auth progress, got %#v", progress)
	}
	if progress[1].Message != expectedReason {
		t.Fatalf("expected skipped auth progress message %q, got %q", expectedReason, progress[1].Message)
	}
	if !reflect.DeepEqual(progress[1].Result, blocked) {
		t.Fatalf("expected skipped auth progress result to match summary result, got %#v", progress[1].Result)
	}
}

func TestGUITrackerAuthSkipReasonIncludesRetryAction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status api.TrackerAuthStatus
		want   string
	}{
		"diagnostic gets action": {
			status: api.TrackerAuthStatus{
				Message:   "stored session expired or invalid",
				LastError: "remote session validation failed",
			},
			want: "tracker auth not ready: stored session expired or invalid: remote session validation failed; configure tracker auth and retry",
		},
		"existing action is not duplicated": {
			status: api.TrackerAuthStatus{
				Message: "stored session expired; configure tracker auth and retry",
			},
			want: "tracker auth not ready: stored session expired; configure tracker auth and retry",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := guiTrackerAuthSkipReason(tt.status); got != tt.want {
				t.Fatalf("guiTrackerAuthSkipReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckGUIDupesWithAuthPreservesReadyTrackerOrder(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
		statuses:     []api.TrackerAuthStatus{{TrackerID: "PTP", State: trackerauth.StateConfigured}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := newTestCore(testCoreOptions{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}})

	_, err := checkWithAuthForTest(context.Background(), coreSvc.dupe, api.InteractionModeInteractive, api.DuplicateSubject{SourcePath: "source.mkv"}, []string{"PTP", "AITHER"})
	if err != nil {
		t.Fatalf("check WebUI dupes with auth: %v", err)
	}
	if !slices.Equal(dupes.trackers, []string{"PTP", "AITHER"}) {
		t.Fatalf("expected input tracker order after auth preflight, got %v", dupes.trackers)
	}
}

func TestCheckGUIDupesWithAuthAvoidsAuthForBlockingRuleFailedTracker(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := newTestCore(testCoreOptions{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}})
	meta := api.DuplicateSubject{
		SourcePath: "source.mkv",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"PTP": {{
				Rule:     "example_rule",
				Reason:   "example rule failed",
				Severity: api.RuleFailureSeverityBlocking,
			}},
		},
	}

	_, err := checkWithAuthForTest(context.Background(), coreSvc.dupe, api.InteractionModeInteractive, meta, []string{"PTP"})
	if err != nil {
		t.Fatalf("check WebUI dupes with auth: %v", err)
	}
	if len(auth.validated) != 0 {
		t.Fatalf("expected terminal rule failure to skip auth validation, got %v", auth.validated)
	}
	if !slices.Equal(dupes.trackers, []string{"PTP"}) {
		t.Fatalf("expected dupe service to retain rule-failed tracker for its skip result, got %v", dupes.trackers)
	}
}

func TestCheckGUIDupesWithAuthStillValidatesWarningOnlyTracker(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
		statuses:     []api.TrackerAuthStatus{{TrackerID: "PTP", State: trackerauth.StateConfigured}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := newTestCore(testCoreOptions{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}})
	meta := api.DuplicateSubject{
		SourcePath: "Example.Release.2026.1080p-GRP.mkv",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"PTP": {{
				Rule:     "example_advice",
				Reason:   "example advisory",
				Severity: api.RuleFailureSeverityWarning,
			}},
		},
	}

	_, err := checkWithAuthForTest(context.Background(), coreSvc.dupe, api.InteractionModeInteractive, meta, []string{"PTP"})
	if err != nil {
		t.Fatalf("check WebUI dupes with auth: %v", err)
	}
	if !slices.Equal(auth.validated, []string{"PTP"}) {
		t.Fatalf("expected warning-only tracker to require managed auth validation, got %v", auth.validated)
	}
	if !slices.Equal(dupes.trackers, []string{"PTP"}) {
		t.Fatalf("expected warning-only tracker to reach dupe search, got %v", dupes.trackers)
	}
}

func TestCheckGUIDupesWithAuthLeavesCLIAuthOwnershipUnchanged(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := newTestCore(testCoreOptions{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}})

	_, err := checkWithAuthForTest(context.Background(), coreSvc.dupe, api.InteractionModeUnattended, api.DuplicateSubject{SourcePath: "source.mkv"}, []string{"PTP"})
	if err != nil {
		t.Fatalf("check CLI dupes: %v", err)
	}
	if len(auth.validated) != 0 {
		t.Fatalf("expected CLI preflight to remain entrypoint-owned, got %v", auth.validated)
	}
}
