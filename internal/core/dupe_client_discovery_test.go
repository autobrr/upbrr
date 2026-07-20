// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type duplicateFactsStub struct {
	subject api.DuplicateSubject
}

func duplicateTestRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry, err := trackerimpl.NewRegistry()
	if err != nil {
		t.Fatalf("create tracker registry: %v", err)
	}
	return registry
}

func (*duplicateFactsStub) Prepare(context.Context, api.PrepareInput) (api.PrepareResult, error) {
	return api.PrepareResult{}, nil
}

func (s *duplicateFactsStub) ResolveDuplicateSubject(context.Context, api.DuplicateCheckInput) (api.DuplicateSubject, error) {
	return s.subject, nil
}

type duplicateAssessmentStub struct {
	calls   int
	subject api.DuplicateSubject
	options dupechecking.CheckOptions
}

func (s *duplicateAssessmentStub) Check(context.Context, api.DuplicateSubject, []string) (api.DupeCheckSummary, error) {
	return api.DupeCheckSummary{}, nil
}

func (s *duplicateAssessmentStub) CheckWithAssessment(
	_ context.Context,
	subject api.DuplicateSubject,
	trackers []string,
	options dupechecking.CheckOptions,
) (api.DupeCheckSummary, dupechecking.Assessment, error) {
	s.calls++
	s.subject = subject
	s.options = options
	results := make([]api.DupeCheckResult, 0, len(trackers))
	for _, tracker := range trackers {
		results = append(results, api.DupeCheckResult{Tracker: tracker})
	}
	return api.DupeCheckSummary{Results: results}, dupechecking.EmptyAssessment(), nil
}

func TestAcceptedDuplicateCheckConsumesPreparedClientEvidenceAndOverlaysExplicitIDs(t *testing.T) {
	t.Parallel()

	checker := &duplicateAssessmentStub{}
	module := newDupeModule(
		config.Config{},
		api.NopLogger{},
		api.ServiceSet{Dupes: checker},
		duplicateTestRegistry(t),
		&duplicateFactsStub{subject: api.DuplicateSubject{
			SourcePath:      "Example.Release.2026.mkv",
			FileList:        []string{"Example.Release.2026.mkv"},
			TrackerIDs:      map[string]string{"btn": "client-id", "aither": "aither-id"},
			MatchedTrackers: []string{"AITHER", "BTN"},
		}},
	)
	_, err := module.checkAccepted(context.Background(), api.DuplicateCheckInput{
		Release:    api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
		Trackers:   []string{"AITHER"},
		TrackerIDs: map[string]string{"btn": "explicit-id"},
	})
	if err != nil {
		t.Fatalf("check accepted: %v", err)
	}
	if checker.calls != 1 {
		t.Fatalf("checker calls = %d", checker.calls)
	}
	if checker.subject.TrackerIDs["btn"] != "explicit-id" || checker.subject.TrackerIDs["aither"] != "aither-id" {
		t.Fatalf("tracker IDs = %#v", checker.subject.TrackerIDs)
	}
	if len(checker.subject.MatchedTrackers) != 2 || checker.subject.MatchedTrackers[0] != "AITHER" || checker.subject.MatchedTrackers[1] != "BTN" {
		t.Fatalf("matched trackers = %#v", checker.subject.MatchedTrackers)
	}
}

func TestAcceptedDuplicateCheckContinuesRemoteAssessmentWithoutPreparedLocalMatch(t *testing.T) {
	t.Parallel()

	checker := &duplicateAssessmentStub{}
	module := newDupeModule(
		config.Config{},
		api.NopLogger{},
		api.ServiceSet{Dupes: checker},
		duplicateTestRegistry(t),
		&duplicateFactsStub{subject: api.DuplicateSubject{SourcePath: "Example.Release.2026.mkv"}},
	)
	_, err := module.checkAccepted(context.Background(), api.DuplicateCheckInput{
		Release:  api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("check accepted: %v", err)
	}
	if checker.calls != 1 || checker.options.SkipRemote {
		t.Fatalf("checker/skip = %d/%t", checker.calls, checker.options.SkipRemote)
	}
}
