// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateRetainsDistinctCandidateRelations(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{
		Names:      []string{"Example.Release.2026.2160p.WEB-DL.DV.HDR-GRP"},
		Resolution: "2160p",
		HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
	}
	candidates := []TrackerCandidate{
		{
			ID:   "1",
			Name: "Example.Release.2026.2160p.WEB-DL.DV.HDR-GRP",
			HDR:  testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
		},
		{
			ID:   "2",
			Name: "Example.Release.2026.2160p.WEB-DL.SDR-GRP",
			HDR:  testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
		},
		{
			ID:   "3",
			Name: "Example.Release.2026.2160p.WEB-DL.Unknown-GRP",
			HDR:  api.HDRFacts{Origin: api.HDREvidenceUnknown, Status: api.HDREvidenceMissing},
		},
	}
	evaluation := Evaluate(target, candidates, trackerspkg.DupePolicy{
		ID:               "example/duplicate/v1",
		RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
	}, SearchEvidence{Complete: true, Pages: 1})

	if len(evaluation.Candidates) != 3 {
		t.Fatalf("candidate evaluations = %#v", evaluation.Candidates)
	}
	if evaluation.Candidates[0].Relation != api.DupeRelationExactDuplicate ||
		evaluation.Candidates[1].Relation != api.DupeRelationCoexists ||
		evaluation.Candidates[2].Relation != api.DupeRelationInsufficientEvidence {
		t.Fatalf("candidate relations = %#v", evaluation.Candidates)
	}
	if !evaluation.Blocks || !evaluation.RequiresAction {
		t.Fatalf("aggregate evaluation = %#v", evaluation)
	}
}

func TestEvaluateANTGenericHDRIsInsufficientForHDR10Plus(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10Plus)}
	candidate := TrackerCandidate{HDR: testHDR(api.HDREvidencePartial, api.HDRFormatHDR10)}
	evaluation := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{
		ID:               "ant/duplicate/v1",
		RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
	}, SearchEvidence{Complete: true})

	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationInsufficientEvidence {
		t.Fatalf("ANT generic HDR relation = %q", got)
	}
}

func TestEvaluateANTPartialGenericHDRDoesNotConfirmSameSlot(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10)},
		[]TrackerCandidate{{HDR: testHDR(api.HDREvidencePartial, api.HDRFormatHDR10)}},
		trackerspkg.DupePolicy{
			ID:               "ant/duplicate/v1",
			RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
		},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationInsufficientEvidence {
		t.Fatalf("ANT partial same-format relation = %q", got)
	}
}

func TestEvaluateNBLUsesCoarseSDRHDRDVSlots(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		ID:               "nbl/duplicate/v1",
		RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
		HDRSlotMode:      trackerspkg.DupeHDRSlotModeGeneric,
	}
	tests := []struct {
		name      string
		target    api.HDRFacts
		candidate api.HDRFacts
		want      api.DupeRelation
	}{
		{
			name:      "sdr and hdr coexist",
			target:    testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
			candidate: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "hdr and dv coexist",
			target:    testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10Plus),
			candidate: testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "dv and dv hdr coexist",
			target:    testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision),
			candidate: testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			want:      api.DupeRelationCoexists,
		},
		{
			name:      "hdr variants share generic hdr slot",
			target:    testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10Plus),
			candidate: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			want:      api.DupeRelationSameSlot,
		},
		{
			name:      "dv hdr variants share combined slot",
			target:    testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus),
			candidate: testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			want:      api.DupeRelationSameSlot,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			evaluation := Evaluate(
				api.TrackerDuplicateTarget{HDR: test.target},
				[]TrackerCandidate{{HDR: test.candidate}},
				policy,
				SearchEvidence{Complete: true},
			)
			if got := evaluation.Candidates[0].Relation; got != test.want {
				t.Fatalf("NBL HDR relation = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEvaluateMTVHDRCompatibilityIsDirectional(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		ID:               "mtv/duplicate/v1",
		RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
	}
	hdr10Candidate := TrackerCandidate{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10)}

	withFallback := api.TrackerDuplicateTarget{
		HDR: api.HDRFacts{
			Formats:         []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
			FallbackFormats: []api.HDRFormat{api.HDRFormatHDR10},
			Origin:          api.HDREvidenceMediaInfo,
			Status:          api.HDREvidenceComplete,
		},
	}
	evaluation := Evaluate(withFallback, []TrackerCandidate{hdr10Candidate}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationProposedTrumps {
		t.Fatalf("DV+HDR -> HDR relation = %q", got)
	}

	dvOnly := api.TrackerDuplicateTarget{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision)}
	evaluation = Evaluate(dvOnly, []TrackerCandidate{hdr10Candidate}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationCoexists {
		t.Fatalf("DV-only -> HDR relation = %q", got)
	}
}

func TestEvaluateRequiredPartialHDRFailsClosedOutsideProvenTitlePolicy(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10)},
		[]TrackerCandidate{{HDR: testHDR(api.HDREvidencePartial, api.HDRFormatHDR10)}},
		trackerspkg.DupePolicy{
			ID:               "bhd/duplicate/v1",
			RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
		},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "candidate_hdr_partial" {
		t.Fatalf("partial HDR relation = %#v", got)
	}
}

func TestEvaluateLUMEDolbyVisionProfileEvidence(t *testing.T) {
	t.Parallel()

	targetHDR := testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision)
	targetHDR.DolbyVisionProfile = "5"
	policy := trackerspkg.DupePolicy{
		ID:               "lume/duplicate/v1",
		RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true},
	}

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{HDR: targetHDR},
		[]TrackerCandidate{{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision)}},
		policy,
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "candidate_dv_profile_missing" {
		t.Fatalf("missing DV profile relation = %#v", got)
	}

	candidateHDR := testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision)
	candidateHDR.DolbyVisionProfile = "7"
	evaluation = Evaluate(
		api.TrackerDuplicateTarget{HDR: targetHDR},
		[]TrackerCandidate{{HDR: candidateHDR}},
		policy,
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationManualReview {
		t.Fatalf("different DV profile relation = %q", got)
	}
}

func TestEvaluateCriticalEvidenceAndStructuralSlotsPrecedeDirectionalHDR(t *testing.T) {
	t.Parallel()

	targetHDR := api.HDRFacts{
		Formats:         []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		FallbackFormats: []api.HDRFormat{api.HDRFormatHDR10},
		Origin:          api.HDREvidenceMediaInfo,
		Status:          api.HDREvidenceComplete,
	}
	policy := trackerspkg.DupePolicy{
		ID: "lume/duplicate/v1",
		RequiredEvidence: trackerspkg.DupeEvidenceRequirements{
			HDR:      true,
			Provider: true,
		},
		SlotDimensions: []trackerspkg.DupeDimension{
			trackerspkg.DupeDimensionProvider,
			trackerspkg.DupeDimensionHDR,
		},
	}
	target := api.TrackerDuplicateTarget{Provider: "Example Provider A", HDR: targetHDR}
	candidate := TrackerCandidate{
		Provider: "Example Provider B",
		HDR:      testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
	}

	evaluation := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationCoexists ||
		got.Reasons[0].Code != "different_provider" {
		t.Fatalf("different provider relation = %#v", got)
	}

	candidate.Provider = ""
	evaluation = Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "candidate_provider_missing" {
		t.Fatalf("missing provider relation = %#v", got)
	}
}

func TestEvaluateMTVMissingFilesPrecedesDirectionalHDR(t *testing.T) {
	t.Parallel()

	targetHDR := api.HDRFacts{
		Formats:         []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		FallbackFormats: []api.HDRFormat{api.HDRFormatHDR10},
		Origin:          api.HDREvidenceMediaInfo,
		Status:          api.HDREvidenceComplete,
	}
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{FileNames: []string{"Example.Release.2026.mkv"}, HDR: targetHDR},
		[]TrackerCandidate{{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10)}},
		trackerspkg.DupePolicy{
			ID:               "mtv/duplicate/v1",
			RequiredEvidence: trackerspkg.DupeEvidenceRequirements{HDR: true, Files: true},
		},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "candidate_files_missing" {
		t.Fatalf("missing MTV files relation = %#v", got)
	}
}

func TestEvaluateSizeVarianceUsesAbsoluteRatioAtBoundary(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{ID: "lume/duplicate/v1", SizeVariancePercent: 20}
	for _, test := range []struct {
		name          string
		targetSize    int64
		candidateSize int64
		want          api.DupeRelation
	}{
		{
			name:          "19.99 percent",
			targetSize:    10000,
			candidateSize: 8001,
			want:          api.DupeRelationSameSlot,
		},
		{
			name:          "20 percent",
			targetSize:    10000,
			candidateSize: 8000,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "reverse 20 percent",
			targetSize:    8000,
			candidateSize: 10000,
			want:          api.DupeRelationCoexists,
		},
		{
			name:          "20.01 percent",
			targetSize:    10000,
			candidateSize: 7999,
			want:          api.DupeRelationCoexists,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluation := Evaluate(
				api.TrackerDuplicateTarget{SizeBytes: test.targetSize},
				[]TrackerCandidate{{SizeKnown: true, SizeBytes: test.candidateSize}},
				policy,
				SearchEvidence{Complete: true},
			)
			if got := evaluation.Candidates[0].Relation; got != test.want {
				t.Fatalf("size relation = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEvaluateSizeVarianceHonorsPolicySlot(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		ID:                      "example/duplicate/v1",
		SizeVariancePercent:     20,
		SizeVarianceResolutions: []string{"1080p"},
	}
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{Resolution: "2160p", SizeBytes: 1000},
		[]TrackerCandidate{{
			Resolution: "2160p",
			SizeKnown:  true,
			SizeBytes:  700,
		}},
		policy,
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationSameSlot {
		t.Fatalf("out-of-scope size relation = %q", got)
	}
}

func TestEvaluateIncompleteEmptySearchRequiresAction(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(api.TrackerDuplicateTarget{}, nil, trackerspkg.DupePolicy{}, SearchEvidence{Complete: false, Pages: 1})
	if !evaluation.RequiresAction || evaluation.Blocks {
		t.Fatalf("incomplete evaluation = %#v", evaluation)
	}
}

func TestEvaluateSeasonPackContainingTargetFileIsNotExactDuplicate(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			Names:     []string{"Example.Show.S01E02.1080p.WEB-DL-GRP"},
			FileNames: []string{"Example.Show.S01E02.mkv"},
			Season:    1,
			Episode:   2,
		},
		[]TrackerCandidate{{
			Name:   "Example.Show.S01.1080p.WEB-DL-GRP",
			Files:  []string{"Example.Show.S01E01.mkv", "Example.Show.S01E02.mkv"},
			Season: 1,
			Pack:   true,
		}},
		trackerspkg.DupePolicy{PrecedenceRules: trackerspkg.SeasonPackPrecedenceRules()},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationExistingPreferred {
		t.Fatalf("season-pack relation = %q", got)
	}
}

func TestEvaluateEpisodeIdentitySeparatesUnrelatedCandidates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate TrackerCandidate
		want      api.DupeRelation
		reason    string
	}{
		{
			name:      "different episode",
			target:    api.TrackerDuplicateTarget{Season: 1, Episode: 2},
			candidate: TrackerCandidate{Season: 1, Episode: 3},
			want:      api.DupeRelationCoexists,
			reason:    "different_episode",
		},
		{
			name:      "missing candidate season",
			target:    api.TrackerDuplicateTarget{Season: 1, Episode: 2},
			candidate: TrackerCandidate{},
			want:      api.DupeRelationInsufficientEvidence,
			reason:    "candidate_season_missing",
		},
		{
			name:      "different daily episode",
			target:    api.TrackerDuplicateTarget{Date: "2026-07-27"},
			candidate: TrackerCandidate{Date: "2026-07-28"},
			want:      api.DupeRelationCoexists,
			reason:    "different_daily_episode",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			evaluation := Evaluate(
				test.target,
				[]TrackerCandidate{test.candidate},
				trackerspkg.DupePolicy{},
				SearchEvidence{Complete: true},
			)
			got := evaluation.Candidates[0]
			if got.Relation != test.want || got.Reasons[0].Code != test.reason {
				t.Fatalf("episode identity result = %#v", got)
			}
		})
	}
}

func TestEvaluateExactFileIdentityNormalizesHostAndTorrentPaths(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			FileNames: []string{
				`C:\media\Example.Release.2026\Example.Release.2026.mkv`,
				`C:\media\Example.Release.2026\Example.Release.2026.srt`,
			},
			SizeBytes: 1000,
		},
		[]TrackerCandidate{{
			Files: []string{
				"Example.Release.2026/Example.Release.2026.srt",
				"Example.Release.2026/Example.Release.2026.mkv",
			},
			SizeKnown: true,
			SizeBytes: 1000,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationExactDuplicate {
		t.Fatalf("exact file relation = %q", got)
	}
}

func TestEvaluateCompatibilityPolicyDoesNotInventPackPrecedence(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{Season: 1, Episode: 2},
		[]TrackerCandidate{{Season: 1, Pack: true}},
		trackerspkg.DupePolicy{ManualReviewRules: []trackerspkg.DupeRule{{
			ID:                 "policy_evidence_unavailable",
			Relation:           "manual_review",
			ReasonCode:         "tracker_policy_not_evidence_backed",
			RequiresManualStep: true,
		}}},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationManualReview {
		t.Fatalf("compatibility pack relation = %q", got)
	}
}

func TestEvaluateRequiredTargetEvidenceFailsClosed(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{},
		[]TrackerCandidate{{Provider: "Example Provider"}},
		trackerspkg.DupePolicy{RequiredEvidence: trackerspkg.DupeEvidenceRequirements{Provider: true}},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "target_provider_missing" {
		t.Fatalf("target evidence relation = %#v", got)
	}
}

func TestEvaluateCompoundDirectionalRule(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{PrecedenceRules: []trackerspkg.DupeRule{{
		ID:       "proposed_webdl_over_webrip",
		Relation: "proposed_trumps",
		Conditions: []trackerspkg.DupeCondition{
			{
				Dimension:       trackerspkg.DupeDimensionType,
				TargetValues:    []string{"WEB-DL"},
				CandidateValues: []string{"WEBRip"},
			},
			{
				Dimension:        trackerspkg.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackerspkg.DupeDimensionProvider,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
		},
	}}}
	target := api.TrackerDuplicateTarget{
		Type:       "WEB-DL",
		Resolution: "1080p",
		Provider:   "Example Provider",
	}

	evaluation := Evaluate(target, []TrackerCandidate{{
		Type:       "WEBRip",
		Resolution: "1080p",
		Provider:   "Example Provider",
	}}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationProposedTrumps {
		t.Fatalf("compound relation = %q", got)
	}

	evaluation = Evaluate(target, []TrackerCandidate{{
		Type: "WEBRip", Resolution: "1080p",
	}}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationSameSlot {
		t.Fatalf("incomplete compound relation = %q", got)
	}
}

func TestNormalizeCandidateUsesTitleHDRForMTVOnly(t *testing.T) {
	t.Parallel()

	entry := api.DupeEntry{Name: "Example.Release.2026.DV.HDR10+.2160p-GRP"}
	if candidate := NormalizeCandidate(entry, "ANT"); candidate.HDR.Status != api.HDREvidenceMissing {
		t.Fatalf("ANT title became HDR evidence: %#v", candidate.HDR)
	}
	candidate := NormalizeCandidate(entry, "MTV")
	if candidate.HDR.Origin != api.HDREvidenceTrackerTitle || candidate.HDR.Status != api.HDREvidencePartial {
		t.Fatalf("MTV title HDR evidence = %#v", candidate.HDR)
	}
}

func TestNormalizeCandidateUsesTitleCoordinatesForAllTrackers(t *testing.T) {
	t.Parallel()

	candidate := NormalizeCandidate(
		api.DupeEntry{Name: "Example.Show.S01E02.2026-07-27.DV.2160p-GRP"},
		"AITHER",
	)
	if candidate.Season != 1 || candidate.Episode != 2 || candidate.Date != "2026-07-27" {
		t.Fatalf("candidate coordinates = %#v", candidate)
	}
	if candidate.HDR.Status != api.HDREvidenceMissing || candidate.Resolution != "" {
		t.Fatalf("non-MTV title became media evidence: %#v", candidate)
	}

	candidate = NormalizeCandidate(
		api.DupeEntry{Name: "Example.Show.S01E02.1080p-GRP", Season: 1},
		"AITHER",
	)
	if candidate.Episode != 2 {
		t.Fatalf("candidate episode = %d", candidate.Episode)
	}
}

func testHDR(status api.HDREvidenceStatus, formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: append([]api.HDRFormat(nil), formats...),
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  status,
	}
}
