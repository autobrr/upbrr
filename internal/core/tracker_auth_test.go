// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackerauth"
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

func (s *trackerAuthPreflightDupeService) Check(_ context.Context, meta api.PreparedMetadata, trackerIDs []string) (api.DupeCheckSummary, error) {
	s.trackers = append([]string(nil), trackerIDs...)
	results := make([]api.DupeCheckResult, 0, len(trackerIDs))
	for _, trackerID := range trackerIDs {
		results = append(results, api.DupeCheckResult{Tracker: trackerID, Status: "completed"})
	}
	return api.DupeCheckSummary{SourcePath: meta.SourcePath, Results: results}, nil
}

func TestCheckGUIDupesWithAuthSkipsBlockedTrackerAndContinuesReadyTrackers(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true, SupportsManual2FA: true}},
		statuses: []api.TrackerAuthStatus{{
			TrackerID: "PTP",
			State:     trackerauth.StateLoginRequired,
			Needs2FA:  true,
			Message:   "manual 2FA required",
		}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := &Core{
		logger: api.NopLogger{},
		services: api.ServiceSet{
			Dupes:       dupes,
			TrackerAuth: auth,
		},
	}
	var progress []api.DupeProgressUpdate
	ctx := api.WithDupeProgressReporter(context.Background(), func(update api.DupeProgressUpdate) {
		progress = append(progress, update)
	})

	summary, err := coreSvc.checkGUIDupesWithAuth(ctx, api.ModeGUI, api.PreparedMetadata{SourcePath: "source.mkv"}, []string{"PTP", "AITHER"})
	if err != nil {
		t.Fatalf("check GUI dupes with auth: %v", err)
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
	expectedReason := "tracker auth not ready: manual 2FA required"
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

func TestCheckGUIDupesWithAuthPreservesReadyTrackerOrder(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
		statuses:     []api.TrackerAuthStatus{{TrackerID: "PTP", State: trackerauth.StateConfigured}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := &Core{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}}

	_, err := coreSvc.checkGUIDupesWithAuth(context.Background(), api.ModeGUI, api.PreparedMetadata{SourcePath: "source.mkv"}, []string{"PTP", "AITHER"})
	if err != nil {
		t.Fatalf("check GUI dupes with auth: %v", err)
	}
	if !slices.Equal(dupes.trackers, []string{"PTP", "AITHER"}) {
		t.Fatalf("expected input tracker order after auth preflight, got %v", dupes.trackers)
	}
}

func TestCheckGUIDupesWithAuthAvoidsAuthForRuleFailedTracker(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := &Core{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}}
	meta := api.PreparedMetadata{
		SourcePath: "source.mkv",
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"PTP": {{Rule: "example_rule", Reason: "example rule failed"}},
		},
	}

	_, err := coreSvc.checkGUIDupesWithAuth(context.Background(), api.ModeGUI, meta, []string{"PTP"})
	if err != nil {
		t.Fatalf("check GUI dupes with auth: %v", err)
	}
	if len(auth.validated) != 0 {
		t.Fatalf("expected terminal rule failure to skip auth validation, got %v", auth.validated)
	}
	if !slices.Equal(dupes.trackers, []string{"PTP"}) {
		t.Fatalf("expected dupe service to retain rule-failed tracker for its skip result, got %v", dupes.trackers)
	}
}

func TestCheckGUIDupesWithAuthLeavesCLIAuthOwnershipUnchanged(t *testing.T) {
	t.Parallel()

	auth := &trackerAuthPreflightTestService{
		capabilities: []api.TrackerAuthCapability{{TrackerID: "PTP", SupportsLogin: true}},
	}
	dupes := &trackerAuthPreflightDupeService{}
	coreSvc := &Core{logger: api.NopLogger{}, services: api.ServiceSet{Dupes: dupes, TrackerAuth: auth}}

	_, err := coreSvc.checkGUIDupesWithAuth(context.Background(), api.ModeCLI, api.PreparedMetadata{SourcePath: "source.mkv"}, []string{"PTP"})
	if err != nil {
		t.Fatalf("check CLI dupes: %v", err)
	}
	if len(auth.validated) != 0 {
		t.Fatalf("expected CLI preflight to remain entrypoint-owned, got %v", auth.validated)
	}
}
