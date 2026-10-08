// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func packReviewPolicy() trackerspkg.DupePolicy {
	return trackerspkg.DupePolicy{
ID: "review/duplicate/v1",
 EvidenceID: "reviewed-pack-rules",
 PackContainmentRequiresReview: true,
		SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionType, trackerspkg.DupeDimensionResolution, trackerspkg.DupeDimensionHDR},
}
}

func TestPackContainmentReviewWarnsWithoutTrumpQualification(t *testing.T) {
	for _, proposedPack := range []bool{false, true} {
		for _, incomplete := range []bool{false, true} {
			target := api.TrackerDuplicateTarget{
Category: "TV",
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 Season: 1,
 Episode: 2,
 Group: "GRP",
 HDR: api.HDRFacts{
Formats: []api.HDRFormat{api.HDRFormatSDR},
 Status: api.HDREvidenceComplete,
 Origin: api.HDREvidenceMediaInfo,
},
}
			candidate := TrackerCandidate{
ID: "candidate",
 Category: "TV",
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 Season: 1,
 Pack: true,
 Group: "OTHER",
 HDR: api.HDRFacts{
Formats: []api.HDRFormat{api.HDRFormatSDR},
 Status: api.HDREvidenceComplete,
 Origin: api.HDREvidenceTrackerAPI,
},
}
			if proposedPack {
				target.Pack, target.Episode, candidate.Pack, candidate.Episode = true, 0, false, 2
			}
			if incomplete {
				candidate.HDR = api.HDRFacts{}
				candidate.Resolution = ""
			}
			result := Evaluate(target, []TrackerCandidate{candidate}, packReviewPolicy(), SearchEvidence{Complete: !incomplete, WorkScope: WorkScopeProviderID})
			got := result.Candidates[0]
			if got.Relation != api.DupeRelationManualReview || !result.RequiresAction || result.Blocks || len(got.Reasons) != 1 || got.Reasons[0].Code != "pack_audio_languages_unverified" || !strings.Contains(got.Reasons[0].Message, "does not establish") {
				t.Fatalf("proposedPack=%v incomplete=%v: missing explicit comparison warning: %#v", proposedPack, incomplete, result)
			}
		}
	}
}

func TestPackContainmentReviewPreservesExactDistinctAndFullDisc(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.TrackerDuplicateTarget, *TrackerCandidate)
		want   api.DupeRelation
	}{
		{"resolution differs", func(_ *api.TrackerDuplicateTarget, c *TrackerCandidate) { c.Resolution = "2160p" }, api.DupeRelationCoexists},
		{"media class differs", func(_ *api.TrackerDuplicateTarget, c *TrackerCandidate) { c.Type = "REMUX"; c.Source = "BluRay" }, api.DupeRelationCoexists},
		{"season differs", func(_ *api.TrackerDuplicateTarget, c *TrackerCandidate) { c.Season = 2 }, api.DupeRelationCoexists},
		{"exact identity", func(s *api.TrackerDuplicateTarget, c *TrackerCandidate) {
			s.Names = []string{"Exact Release"}
			c.Name = "Exact Release"
		}, api.DupeRelationExactDuplicate},
		{"full discs retain preference", func(s *api.TrackerDuplicateTarget, c *TrackerCandidate) {
			s.Type, s.Source, c.Type, c.Source = "DISC", "BluRay", "DISC", "BluRay"
		}, api.DupeRelationExistingPreferred},
		{"full disc packs retain preference", func(s *api.TrackerDuplicateTarget, c *TrackerCandidate) {
			s.Type, s.Source, c.Type, c.Source = "DISC", "BluRay", "DISC", "BluRay"
			s.Pack, s.Episode, c.Pack, c.Episode = true, 0, false, 2
		}, api.DupeRelationCoexists},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := api.TrackerDuplicateTarget{
Category: "TV",
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 Season: 1,
 Episode: 2,
}
			candidate := TrackerCandidate{
Category: "TV",
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 Season: 1,
 Pack: true,
}
			test.change(&target, &candidate)
			result := Evaluate(target, []TrackerCandidate{candidate}, packReviewPolicy(), SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if got := result.Candidates[0]; got.Relation != test.want {
				t.Fatalf("relation = %#v, want %s", got, test.want)
			}
		})
	}
	result := Evaluate(api.TrackerDuplicateTarget{
Names: []string{"Exact Release"},
 Category: "TV",
 Type: "WEBDL",
 Season: 1,
 Pack: true,
}, []TrackerCandidate{
		{
ID: "episode",
 Category: "TV",
 Type: "WEBDL",
 Season: 1,
 Episode: 2,
},
		{
ID: "exact",
 Name: "Exact Release",
 Category: "TV",
 Type: "WEBDL",
 Season: 1,
 Pack: true,
},
	}, packReviewPolicy(), SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if !result.Blocks || result.RequiresAction {
		t.Fatalf("manual review hid an independent exact block: %#v", result)
	}
}

func TestPackReviewApprovalRemainsTrackerGenerationAndEvidenceBound(t *testing.T) {
	meta := api.DuplicateSubject{
SourcePath: "Example Pack",
 Identity: api.ExternalIdentity{SourcePath: "Example Pack", Generation: 1},
		Projection:      &api.TrackerReleaseProjection{
TrackerID: "SP",
 DuplicateSearchFingerprint: "search-one",
 DuplicatePolicyFingerprint: "policy-three",
},
		BlockedTrackers: map[string][]api.TrackerBlockReason{"SP": {api.TrackerBlockReasonDupe, api.TrackerBlockReasonClaim}},
}
	cfg := config.Config{}
	evidence := AssessmentEvidence{
Tracker: "SP",
 Disposition: DispositionResolved,
 HasDupes: true,
		Match: api.DupeMatch{MatchedReason: "pack_audio_languages_unverified", MatchedID: "episode-one"},
}
	assessment := NewAssessment(meta, cfg, []AssessmentEvidence{evidence})
	authorized, err := assessment.Authorize(meta, cfg, []string{"SP"})
	if err != nil {
		t.Fatal(err)
	}
	decision, _ := authorized.Decision("SP")
	if decision.Verdict != VerdictOverridden {
		t.Fatalf("review approval = %#v", decision)
	}
	authorized.Apply(&meta)
	if !slices.Equal(meta.BlockedTrackers["SP"], []api.TrackerBlockReason{api.TrackerBlockReasonClaim}) {
		t.Fatalf("approval removed independent block: %#v", meta.BlockedTrackers)
	}
	changed := meta
	changed.Identity.Generation++
	if _, err := authorized.Authorize(changed, cfg, []string{"SP"}); err == nil {
		t.Fatal("stale generation authorized")
	}
	changed = meta
	projection := *meta.Projection
	projection.DuplicateSearchFingerprint = "search-two"
	changed.Projection = &projection
	if _, err := authorized.Authorize(changed, cfg, []string{"SP"}); err == nil {
		t.Fatal("changed search scope authorized")
	}
	if _, err := assessment.Authorize(meta, cfg, []string{"OTHER"}); err == nil {
		t.Fatal("warning acknowledgement crossed trackers")
	}
	evidence.Match.MatchedID = "episode-two"
	refreshed := authorized.Merge(NewAssessment(meta, cfg, []AssessmentEvidence{evidence}), []string{"SP"})
	decision, _ = refreshed.Decision("SP")
	if decision.Verdict != VerdictBlocked || decision.Authorization != AuthorizationNone {
		t.Fatalf("refreshed search retained approval: %#v", decision)
	}
	evidence.Match.MatchedReason = "in_client"
	if _, err := NewAssessment(meta, cfg, []AssessmentEvidence{evidence}).Authorize(meta, cfg, []string{"SP"}); err == nil {
		t.Fatal("in-client match was waived")
	}
}
