// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNDuplicatePolicyRelations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate dupe.TrackerCandidate
		want      api.DupeRelation
	}{
		{
			name:      "scene and p2p coexist",
			target:    btnPolicyTarget("WEB-DL", "1080p", "H.264", "Scene"),
			candidate: btnPolicyCandidate("WEB-DL", "1080p", "H.264", "P2P"),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "p2p and scene coexist",
			target:    btnPolicyTarget("WEB-DL", "1080p", "H.264", "P2P"),
			candidate: btnPolicyCandidate("WEB-DL", "1080p", "H.264", "Scene"),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "web h265 and h264 coexist",
			target:    btnPolicyTarget("WEB-DL", "1080p", "H.265", "P2P"),
			candidate: btnPolicyCandidate("WEB-DL", "1080p", "H.264", "P2P"),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "web h264 and h265 coexist",
			target:    btnPolicyTarget("WEB-DL", "1080p", "H.264", "P2P"),
			candidate: btnPolicyCandidate("WEB-DL", "1080p", "H.265", "P2P"),
			want:      api.DupeRelationCoexists,
		},
		{
			name: "web without region ignores pal ntsc",
			target: func() api.TrackerDuplicateTarget {
				target := btnPolicyTarget("WEB-DL", "1080p", "H.264", "P2P")
				target.Names = []string{"Example.Show.S01E12.PAL.WEB-DL.H.264-GRP"}
				target.Episode = 12
				return target
			}(),
			candidate: func() dupe.TrackerCandidate {
				candidate := btnPolicyCandidate("WEB-DL", "1080p", "H.264", "P2P")
				candidate.Name = "Example.Show.S01E12.NTSC.WEB-DL.H.264-GRP"
				candidate.Episode = 12
				return candidate
			}(),
			want: api.DupeRelationCoexists,
		},
		{
			name:      "bluray and dvd coexist",
			target:    btnPolicyTarget("BluRay", "576p", "H.264", "P2P"),
			candidate: btnPolicyCandidate("DVD", "576p", "H.264", "P2P"),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "dvd and bluray coexist",
			target:    btnPolicyTarget("DVD", "576p", "H.264", "P2P"),
			candidate: btnPolicyCandidate("BluRay", "576p", "H.264", "P2P"),
			want:      api.DupeRelationCoexists,
		},
		{
			name: "pal and ntsc coexist",
			target: func() api.TrackerDuplicateTarget {
				target := btnPolicyTarget("DVD", "576i", "MPEG2", "P2P")
				target.Names = []string{"Example.Show.S01.PAL.DVD-GRP"}
				return target
			}(),
			candidate: func() dupe.TrackerCandidate {
				candidate := btnPolicyCandidate("DVD", "576i", "MPEG2", "P2P")
				candidate.Name = "Example.Show.S01.NTSC.DVD-GRP"
				return candidate
			}(),
			want: api.DupeRelationCoexists,
		},
		{
			name:      "proposed h264 trumps xvid",
			target:    btnPolicyTarget("HDTV", "720p", "H.264", "P2P"),
			candidate: btnPolicyCandidate("HDTV", "720p", "XViD", "P2P"),
			want:      api.DupeRelationProposedTrumps,
		},
		{
			name:      "existing h264 preferred over xvid",
			target:    btnPolicyTarget("HDTV", "720p", "XViD", "P2P"),
			candidate: btnPolicyCandidate("HDTV", "720p", "H.264", "P2P"),
			want:      api.DupeRelationExistingPreferred,
		},
		{
			name: "mixed pack requires review",
			target: func() api.TrackerDuplicateTarget {
				target := btnPolicyTarget("WEB-DL", "1080p", "H.264", "Mixed")
				target.Pack = true
				return target
			}(),
			candidate: btnPolicyCandidate("WEB-DL", "1080p", "H.264", "P2P"),
			want:      api.DupeRelationManualReview,
		},
		{
			name:   "existing mixed pack requires review",
			target: btnPolicyTarget("WEB-DL", "1080p", "H.264", "P2P"),
			candidate: func() dupe.TrackerCandidate {
				candidate := btnPolicyCandidate("WEB-DL", "1080p", "H.264", "Mixed")
				candidate.Pack = true
				return candidate
			}(),
			want: api.DupeRelationManualReview,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := dupe.Evaluate(
				test.target,
				[]dupe.TrackerCandidate{test.candidate},
				*duplicatePolicy(),
				dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID},
			).Candidates[0]
			if got.Relation != test.want {
				t.Fatalf("relation = %#v, want %s", got, test.want)
			}
		})
	}
}

func TestBTNDuplicatePolicySeasonPackCapacity(t *testing.T) {
	t.Parallel()

	policy := *duplicatePolicy()
	target := btnPolicyTarget("HDTV", "1080p", "H.264", "P2P")
	target.Pack = true
	first := btnPolicyCandidate("HDTV", "1080p", "H.264", "P2P")
	first.ID, first.Pack = "1", true
	second := first
	second.ID = "2"

	below := dupe.Evaluate(target, []dupe.TrackerCandidate{first}, policy, btnCompleteSearch())
	if finding := btnSetFinding(t, below, "standalone/btn/duplicate/v1/p2p_season_pack_capacity"); finding.Relation != api.DupeRelationCoexists {
		t.Fatalf("below-capacity finding = %#v", finding)
	}
	full := dupe.Evaluate(target, []dupe.TrackerCandidate{second, first}, policy, btnCompleteSearch())
	if finding := btnSetFinding(t, full, "standalone/btn/duplicate/v1/p2p_season_pack_capacity"); finding.Relation != api.DupeRelationManualReview || !slices.Equal(finding.CandidateIDs, []string{"1", "2"}) {
		t.Fatalf("full finding = %#v", finding)
	}
	incomplete := dupe.Evaluate(target, []dupe.TrackerCandidate{first}, policy, dupe.SearchEvidence{Complete: false})
	if finding := btnSetFinding(t, incomplete, "standalone/btn/duplicate/v1/p2p_season_pack_capacity"); finding.Relation != api.DupeRelationInsufficientEvidence {
		t.Fatalf("incomplete finding = %#v", finding)
	}
	otherSeason := first
	otherSeason.ID, otherSeason.Season = "3", 2
	seasons := dupe.Evaluate(target, []dupe.TrackerCandidate{first, otherSeason}, policy, btnCompleteSearch())
	if finding := btnSetFinding(t, seasons, "standalone/btn/duplicate/v1/p2p_season_pack_capacity"); finding.ExistingOccupancy != 1 {
		t.Fatalf("season capacity included another season: %#v", finding)
	}

	sceneTarget := target
	sceneTarget.ReleaseOrigin = "Scene"
	scene := first
	scene.ReleaseOrigin = "Scene"
	separated := dupe.Evaluate(sceneTarget, []dupe.TrackerCandidate{first, scene}, policy, btnCompleteSearch())
	finding := btnSetFinding(t, separated, "standalone/btn/duplicate/v1/scene_season_pack_capacity")
	if finding.ExistingOccupancy != 1 || !slices.Equal(finding.CandidateIDs, []string{"1"}) {
		t.Fatalf("scene capacity included P2P release: %#v", finding)
	}
}

func TestBTNDuplicatePolicySeasonPackContainmentUsesResolutionSlot(t *testing.T) {
	t.Parallel()

	target := btnPolicyTarget("WEB-DL", "2160p", "H.265", "P2P")
	target.Episode = 1
	pack720 := btnPolicyCandidate("WEB-DL", "720p", "H.264", "P2P")
	pack720.ID, pack720.Pack = "720", true
	pack1080 := btnPolicyCandidate("WEB-DL", "1080p", "H.264", "P2P")
	pack1080.ID, pack1080.Pack = "1080", true
	pack2160 := btnPolicyCandidate("WEB-DL", "2160p", "H.265", "P2P")
	pack2160.ID, pack2160.Pack = "2160", true

	result := dupe.Evaluate(target, []dupe.TrackerCandidate{pack720, pack1080, pack2160}, *duplicatePolicy(), btnCompleteSearch())
	if !result.Blocks || result.RequiresAction || result.Candidates[0].Candidate.ID != "2160" ||
		result.Candidates[0].Relation != api.DupeRelationExistingPreferred ||
		result.Candidates[1].Relation != api.DupeRelationCoexists || result.Candidates[2].Relation != api.DupeRelationCoexists {
		t.Fatalf("season-pack resolution slots = %#v", result)
	}
}

func TestBTNDuplicatePolicyWEBCapacityRequiresProviderEvidence(t *testing.T) {
	t.Parallel()

	target := btnPolicyTarget("WEB-DL", "1080p", "H.264", "None")
	target.Pack, target.Provider = true, "ExampleService"
	first := btnPolicyCandidate("WEB-DL", "1080p", "H.264", "None")
	first.ID, first.Pack, first.Provider = "1", true, "ExampleService"
	second := first
	second.ID = "2"
	below := dupe.Evaluate(target, []dupe.TrackerCandidate{first}, *duplicatePolicy(), btnCompleteSearch())
	if finding := btnSetFinding(t, below, "standalone/btn/duplicate/v1/web_hd_capacity"); finding.Relation != api.DupeRelationCoexists {
		t.Fatalf("WEB below-capacity finding = %#v", finding)
	}
	full := dupe.Evaluate(target, []dupe.TrackerCandidate{first, second}, *duplicatePolicy(), btnCompleteSearch())
	if finding := btnSetFinding(t, full, "standalone/btn/duplicate/v1/web_hd_capacity"); finding.Relation != api.DupeRelationManualReview {
		t.Fatalf("WEB full finding = %#v", finding)
	}
	uhdTarget := target
	uhdTarget.Resolution = "2160p"
	uhd := first
	uhd.Resolution = "2160p"
	uhdFull := dupe.Evaluate(uhdTarget, []dupe.TrackerCandidate{uhd}, *duplicatePolicy(), btnCompleteSearch())
	if finding := btnSetFinding(t, uhdFull, "standalone/btn/duplicate/v1/web_sd_uhd_capacity"); finding.Relation != api.DupeRelationManualReview {
		t.Fatalf("WEB UHD full finding = %#v", finding)
	}
	coarseTarget := target
	coarseTarget.Resolution = "480p"
	coarse := first
	coarse.Resolution = "SD"
	coarseResult := dupe.Evaluate(coarseTarget, []dupe.TrackerCandidate{coarse}, *duplicatePolicy(), btnCompleteSearch())
	if finding := btnSetFinding(t, coarseResult, "standalone/btn/duplicate/v1/web_sd_uhd_capacity"); finding.Relation != api.DupeRelationInsufficientEvidence {
		t.Fatalf("WEB coarse-SD finding = %#v", finding)
	}

	first.Provider = ""
	missing := dupe.Evaluate(target, []dupe.TrackerCandidate{first}, *duplicatePolicy(), btnCompleteSearch())
	if finding := btnSetFinding(t, missing, "standalone/btn/duplicate/v1/web_hd_capacity"); finding.Relation != api.DupeRelationInsufficientEvidence {
		t.Fatalf("WEB missing-provider finding = %#v", finding)
	}
}

func TestBTNDuplicatePolicyRequiresCompleteAutomaticFacts(t *testing.T) {
	t.Parallel()

	target := btnPolicyTarget("WEB-DL", "1080p", "H.265", "P2P")
	candidate := btnPolicyCandidate("WEB-DL", "1080p", "", "P2P")
	candidate.Name = "Example.Show.S01E01.1080p.WEB-DL.H.264-GRP"
	result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *duplicatePolicy(), btnCompleteSearch()).Candidates[0]
	if result.Relation == api.DupeRelationCoexists {
		t.Fatalf("partial codec evidence proved coexistence: %#v", result)
	}
}

func btnPolicyTarget(source string, resolution string, codec string, origin string) api.TrackerDuplicateTarget {
	return api.TrackerDuplicateTarget{
		Source:        source,
		Resolution:    resolution,
		VideoEncode:   codec,
		ReleaseOrigin: origin,
		Season:        1,
	}
}

func btnPolicyCandidate(source string, resolution string, codec string, origin string) dupe.TrackerCandidate {
	return dupe.TrackerCandidate{
		Source:        source,
		Resolution:    resolution,
		Codec:         codec,
		ReleaseOrigin: origin,
		Season:        1,
	}
}

func btnCompleteSearch() dupe.SearchEvidence {
	return dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}
}

func btnSetFinding(t *testing.T, evaluation dupe.Evaluation, ruleID string) dupe.SetFinding {
	t.Helper()
	for _, finding := range evaluation.SetFindings {
		if finding.RuleID == ruleID {
			return finding
		}
	}
	t.Fatalf("set finding %q missing from %#v", ruleID, evaluation.SetFindings)
	return dupe.SetFinding{}
}
