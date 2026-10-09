// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAssessmentSeparatesDispositionFromUploadVerdict(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{SourcePath: "Example.Release.2026", ReleaseName: "Example.Release.2026.1080p-GRP"}
	assessment := NewAssessment(meta, config.Config{}, []AssessmentEvidence{
		{Tracker: "CLEAR", Disposition: DispositionResolved},
		{
			Tracker:     "CANDIDATE",
			Disposition: DispositionResolved,
			HasDupes:    true,
			Match:       api.DupeMatch{MatchedReason: "remote_candidate"},
		},
		{
			Tracker:     "CLIENT",
			Disposition: DispositionResolved,
			HasDupes:    true,
			Match:       api.DupeMatch{MatchedReason: "in_client"},
		},
		{
			Tracker:     "CLIENT_STRUCTURAL",
			Disposition: DispositionResolved,
			Match:       api.DupeMatch{MatchedReason: "in_client"},
		},
		{
			Tracker:     "INCOMPLETE",
			Disposition: DispositionResolved,
			Match:       api.DupeMatch{MatchedReason: "incomplete_search"},
		},
		{
			Tracker:     "NOTRUN",
			Disposition: DispositionNotRun,
			Code:        NotRunMissingCredentials,
		},
		{
			Tracker:     "FAILED",
			Disposition: DispositionFailed,
			Code:        FailureInternal,
		},
	})
	want := map[string]Verdict{
		"CLEAR":             VerdictClear,
		"CANDIDATE":         VerdictBlocked,
		"CLIENT":            VerdictBlocked,
		"CLIENT_STRUCTURAL": VerdictBlocked,
		"INCOMPLETE":        VerdictBlocked,
		"NOTRUN":            VerdictBlocked,
		"FAILED":            VerdictBlocked,
	}
	for tracker, verdict := range want {
		decision, ok := assessment.Decision(tracker)
		if !ok || decision.Verdict != verdict {
			t.Errorf("%s decision = %#v, %t; want %s", tracker, decision, ok, verdict)
		}
	}
}

func TestAssessmentAuthorizationIsOutcomeBoundAndInClientCannotBeOverridden(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{SourcePath: "Example.Release.2026", ReleaseName: "Example.Release.2026.1080p-GRP"}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"CANDIDATE": {APIKey: "candidate-key"},
	}}}
	assessment := NewAssessment(meta, cfg, []AssessmentEvidence{
		{
			Tracker:     "CANDIDATE",
			Disposition: DispositionResolved,
			HasDupes:    true,
			Match:       api.DupeMatch{MatchedReason: "remote_candidate"},
		},
		{
			Tracker:     "NOTRUN",
			Disposition: DispositionNotRun,
			Code:        NotRunMissingCredentials,
		},
		{
			Tracker:     "CLIENT",
			Disposition: DispositionResolved,
			HasDupes:    true,
			Match:       api.DupeMatch{MatchedReason: "in_client"},
		},
		{Tracker: "CLEAR", Disposition: DispositionResolved},
	})
	authorized, err := assessment.Authorize(meta, cfg, []string{"CANDIDATE", "NOTRUN"})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	for tracker, want := range map[string]Verdict{"CANDIDATE": VerdictOverridden, "NOTRUN": VerdictWaived} {
		decision, _ := authorized.Decision(tracker)
		if decision.Verdict != want {
			t.Errorf("%s verdict = %s, want %s", tracker, decision.Verdict, want)
		}
	}
	if _, err := assessment.Authorize(meta, cfg, []string{"CLIENT"}); err == nil {
		t.Fatal("in-client authorization succeeded")
	}
	if _, err := assessment.Authorize(meta, cfg, []string{"CLEAR"}); err == nil {
		t.Fatal("clear authorization succeeded")
	}
	changed := cfg
	changed.Trackers.Trackers = map[string]config.TrackerConfig{"CANDIDATE": {APIKey: "changed-key"}}
	if _, err := assessment.Authorize(meta, changed, []string{"CANDIDATE"}); err == nil {
		t.Fatal("stale authorization succeeded")
	}
	inClientNow := meta
	inClientNow.MatchedTrackers = []string{"CANDIDATE"}
	if _, err := assessment.Authorize(inClientNow, cfg, []string{"CANDIDATE"}); err == nil {
		t.Fatal("authorization survived a new in-client association")
	}
	presentationOnly := meta
	presentationOnly.BlockedTrackers = map[string][]api.TrackerBlockReason{"OTHER": {api.TrackerBlockReasonClaim}}
	if _, err := assessment.Authorize(presentationOnly, cfg, []string{"CANDIDATE"}); err != nil {
		t.Fatalf("presentation-only change invalidated assessment: %v", err)
	}
}

func TestClearAssessmentEntriesClearOwnedProjection(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"AITHER": {api.TrackerBlockReasonDupe, api.TrackerBlockReasonClaim},
		},
		CrossSeedTorrents: []api.UploadedTorrent{{Tracker: "AITHER", DownloadURL: "https://tracker.example/download?token=private"}},
	}
	NewAssessment(meta, config.Config{}, []AssessmentEvidence{{
		Tracker:     "AITHER",
		Disposition: DispositionResolved,
	}}).Apply(&meta)
	if !slices.Equal(meta.BlockedTrackers["AITHER"], []api.TrackerBlockReason{api.TrackerBlockReasonClaim}) {
		t.Fatalf("owned dupe projection not cleared: %#v", meta.BlockedTrackers)
	}
	if len(meta.CrossSeedTorrents) != 0 {
		t.Fatalf("owned cross-seed projection not cleared: %#v", meta.CrossSeedTorrents)
	}
}

func TestAssessmentRetainValidAndApplyUseOnlyBoundPrivateState(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{SourcePath: "Example.Release.2026", ReleaseName: "Example.Release.2026.1080p-GRP"}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"AITHER": {APIKey: "aither-key"},
		"BLU":    {APIKey: "blu-key"},
	}}}
	assessment := NewAssessment(meta, cfg, []AssessmentEvidence{
		{
			Tracker:     "AITHER",
			Disposition: DispositionResolved,
			HasDupes:    true,
			Match: api.DupeMatch{
				MatchedID:       "123",
				MatchedLink:     "https://aither.example/torrents/123",
				MatchedDownload: "https://aither.example/download/123?token=private",
			},
		},
		{Tracker: "BLU", Disposition: DispositionResolved},
	})
	changed := cfg
	changed.Trackers.Trackers = map[string]config.TrackerConfig{
		"AITHER": {APIKey: "changed-key"},
		"BLU":    {APIKey: "blu-key"},
	}
	retained := assessment.RetainValid(meta, changed)
	if _, ok := retained.Decision("AITHER"); ok {
		t.Fatal("changed tracker assessment retained")
	}
	if _, ok := retained.Decision("BLU"); !ok {
		t.Fatal("unrelated tracker assessment dropped")
	}

	projected := meta
	assessment.Apply(&projected)
	if !slices.Contains(projected.BlockedTrackers["AITHER"], api.TrackerBlockReasonDupe) {
		t.Fatalf("dupe block missing: %#v", projected.BlockedTrackers)
	}
	if len(projected.CrossSeedTorrents) != 1 || projected.CrossSeedTorrents[0].DownloadURL != "https://aither.example/download/123?token=private" {
		t.Fatalf("private cross-seed evidence lost: %#v", projected.CrossSeedTorrents)
	}
}

func TestAssessmentTargetReviewIdentityAndIsolation(t *testing.T) {
	reasons := []api.DupeReason{{Code: "source_review", Message: "Verify source eligibility."}}
	entry := newAssessmentEntry(api.DuplicateSubject{}, config.Config{}, "EXAMPLE", DispositionResolved, "", true, api.DupeMatch{}, nil, reasons...)
	original := entry.outcomeID
	reasons[0].Message = "Changed caller review."
	if entry.reviewReasons[0].Message == reasons[0].Message {
		t.Fatal("assessment aliases supplied review")
	}
	cloned := cloneAssessmentEntry(entry)
	cloned.reviewReasons[0].Message = "Changed review requirement."
	if outcomeIdentity(cloned) == original {
		t.Fatal("changed target review retained outcome authority")
	}
	if outcomeIdentity(entry) != original {
		t.Fatal("cloned review aliases original assessment")
	}
	empty := newAssessmentEntry(api.DuplicateSubject{}, config.Config{}, "EXAMPLE", DispositionResolved, "", true, api.DupeMatch{}, nil)
	explicitEmpty := newAssessmentEntry(api.DuplicateSubject{}, config.Config{}, "EXAMPLE", DispositionResolved, "", true, api.DupeMatch{}, nil, []api.DupeReason{}...)
	if empty.outcomeID != explicitEmpty.outcomeID || empty.outcomeID == original {
		t.Fatal("review outcome identity is not stable for empty and distinct for nonempty")
	}
}

func TestAssessmentTargetReviewPersistsAndRequiresAuthorization(t *testing.T) {
	meta, cfg := api.DuplicateSubject{}, config.Config{}
	reasons := []api.DupeReason{{Code: "source_review", Message: "Verify source eligibility."}}
	assessment := NewAssessment(meta, cfg, []AssessmentEvidence{{
		Tracker:       "EXAMPLE",
		Disposition:   DispositionResolved,
		ReviewReasons: reasons,
	}})
	if assessment.entries["EXAMPLE"].verdict != VerdictBlocked {
		t.Fatal("target review was implicitly cleared")
	}
	authorized, err := assessment.Authorize(meta, cfg, []string{"EXAMPLE"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := authorized.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalAssessment(payload)
	if err != nil {
		t.Fatal(err)
	}
	entry := restored.entries["EXAMPLE"]
	if !slices.Equal(entry.reviewReasons, reasons) || outcomeIdentity(entry) != entry.outcomeID || entry.verdict != VerdictOverridden {
		t.Fatal("target review authority lost in persistence")
	}
	changed := NewAssessment(meta, cfg, []AssessmentEvidence{{
		Tracker:       "EXAMPLE",
		Disposition:   DispositionResolved,
		ReviewReasons: []api.DupeReason{{Code: "staff_review", Message: "Verify staff approval."}},
	}})
	if merged := restored.Merge(changed, []string{"EXAMPLE"}); merged.entries["EXAMPLE"].verdict != VerdictBlocked || merged.entries["EXAMPLE"].authorization != AuthorizationNone {
		t.Fatal("changed target review retained authorization")
	}
}
