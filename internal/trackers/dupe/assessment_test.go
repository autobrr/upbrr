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
			Tracker:     "NOTRUN",
			Disposition: DispositionNotRun,
			Code:        NotRunRuleFailed,
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
		"CANDIDATE": {URL: "https://tracker.example"},
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
			Code:        NotRunRuleFailed,
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
	changed.Trackers.Trackers = map[string]config.TrackerConfig{"CANDIDATE": {URL: "https://changed.example"}}
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
		"AITHER": {URL: "https://aither.example"},
		"BLU":    {URL: "https://blu.example"},
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
		"AITHER": {URL: "https://changed.example"},
		"BLU":    {URL: "https://blu.example"},
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
