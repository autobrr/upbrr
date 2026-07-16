// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	dupechecking "github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

type duplicateFactsStub struct {
	subject api.DuplicateSubject
}

func (*duplicateFactsStub) Prepare(context.Context, api.PrepareInput) (api.PrepareResult, error) {
	return api.PrepareResult{}, nil
}

func (s *duplicateFactsStub) ResolveDuplicateSubject(context.Context, api.DuplicateCheckInput) (api.DuplicateSubject, error) {
	return s.subject, nil
}

type duplicateClientStub struct {
	calls  int
	result api.ClientSearchResult
}

func (*duplicateClientStub) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (s *duplicateClientStub) SearchPathedTorrents(context.Context, api.ClientSubject) (api.ClientSearchResult, error) {
	s.calls++
	return s.result, nil
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

func TestAcceptedDuplicateCheckRefreshesCurrentClientEvidenceOnce(t *testing.T) {
	t.Parallel()

	client := &duplicateClientStub{result: api.ClientSearchResult{
		TrackerIDs:      map[string]string{"btn": "client-id", "aither": "aither-id"},
		MatchedTrackers: []string{"btn", "AITHER", "BTN"},
	}}
	checker := &duplicateAssessmentStub{}
	module := newDupeModule(
		config.Config{},
		api.NopLogger{},
		api.ServiceSet{Dupes: checker},
		nil,
		&duplicateFactsStub{subject: api.DuplicateSubject{
			SourcePath: "Example.Release.2026.mkv",
			FileList:   []string{"Example.Release.2026.mkv"},
		}},
		clientdiscovery.New(client, api.NopLogger{}),
	)
	_, err := module.checkAccepted(context.Background(), api.DuplicateCheckInput{
		Release:    api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
		Trackers:   []string{"AITHER"},
		TrackerIDs: map[string]string{"btn": "explicit-id"},
	})
	if err != nil {
		t.Fatalf("check accepted: %v", err)
	}
	if client.calls != 1 || checker.calls != 1 {
		t.Fatalf("client/checker calls = %d/%d", client.calls, checker.calls)
	}
	if checker.subject.TrackerIDs["btn"] != "explicit-id" || checker.subject.TrackerIDs["aither"] != "aither-id" {
		t.Fatalf("tracker IDs = %#v", checker.subject.TrackerIDs)
	}
	if len(checker.subject.MatchedTrackers) != 2 || checker.subject.MatchedTrackers[0] != "AITHER" || checker.subject.MatchedTrackers[1] != "BTN" {
		t.Fatalf("matched trackers = %#v", checker.subject.MatchedTrackers)
	}
}

func TestAcceptedDuplicateCheckContinuesRemoteAssessmentWithoutLocalMatch(t *testing.T) {
	t.Parallel()

	client := &duplicateClientStub{}
	checker := &duplicateAssessmentStub{}
	module := newDupeModule(
		config.Config{},
		api.NopLogger{},
		api.ServiceSet{Dupes: checker},
		nil,
		&duplicateFactsStub{subject: api.DuplicateSubject{SourcePath: "Example.Release.2026.mkv"}},
		clientdiscovery.New(client, api.NopLogger{}),
	)
	_, err := module.checkAccepted(context.Background(), api.DuplicateCheckInput{
		Release:  api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1},
		Trackers: []string{"AITHER"},
	})
	if err != nil {
		t.Fatalf("check accepted: %v", err)
	}
	if client.calls != 1 || checker.calls != 1 || checker.options.SkipRemote {
		t.Fatalf("client/checker/skip = %d/%d/%t", client.calls, checker.calls, checker.options.SkipRemote)
	}
}
