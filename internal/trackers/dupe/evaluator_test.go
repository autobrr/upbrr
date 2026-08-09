// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
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
		ID:             "example/duplicate/v2",
		SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
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

func TestEvaluateReleaseNameIdentity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate string
		wantExact bool
	}{
		{
			name: "single file stem",
			target: api.TrackerDuplicateTarget{
				Names:     []string{"Example Release 2026 Generated-GRP"},
				FileNames: []string{"Example.Release.2026.1080p-GRP.mkv"},
			},
			candidate: "Example.Release.2026.1080p-GRP",
			wantExact: true,
		},
		{
			name:      "different dotted release names",
			target:    api.TrackerDuplicateTarget{Names: []string{"Example.Release.2026.Generated-GRP"}},
			candidate: "Example.Release.2026.Original-GRP",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := Evaluate(
				test.target,
				[]TrackerCandidate{{Name: test.candidate}},
				trackerspkg.DupePolicy{},
				SearchEvidence{Complete: true},
			).Candidates[0].Relation
			if (got == api.DupeRelationExactDuplicate) != test.wantExact {
				t.Fatalf("release-name relation = %q", got)
			}
		})
	}
}

func TestEvaluateANTGenericHDRIsInsufficientForHDR10Plus(t *testing.T) {
	t.Parallel()

	target := api.TrackerDuplicateTarget{HDR: testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10Plus)}
	candidate := TrackerCandidate{HDR: testHDR(api.HDREvidencePartial, api.HDRFormatHDR10)}
	evaluation := Evaluate(target, []TrackerCandidate{candidate}, trackerspkg.DupePolicy{
		ID:             "ant/duplicate/v2",
		SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
		HDRPartialMode: trackerspkg.DupeHDRPartialGenericMarker,
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
			ID:             "ant/duplicate/v2",
			SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
			HDRPartialMode: trackerspkg.DupeHDRPartialGenericMarker,
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
		ID:             "nbl/duplicate/v2",
		SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
		HDRSlotMode:    trackerspkg.DupeHDRSlotModeGeneric,
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

func TestEvaluateANTHDRCompatibilityIsDirectional(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		ID:                   "ant/duplicate/v3",
		SlotDimensions:       []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
		HDRCompatibilityMode: trackerspkg.DupeHDRCompatibilityDirectional,
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
			ID:             "bhd/duplicate/v2",
			SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
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
		ID:                        "lume/duplicate/v2",
		SlotDimensions:            []trackerspkg.DupeDimension{trackerspkg.DupeDimensionHDR},
		RequireDolbyVisionProfile: true,
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
		ID: "lume/duplicate/v2",
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
	candidate.HDR = targetHDR
	evaluation = Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true})
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "candidate_provider_missing" {
		t.Fatalf("missing provider relation = %#v", got)
	}
}

func TestEvaluateANTMissingResolutionPrecedesDirectionalHDR(t *testing.T) {
	t.Parallel()

	targetHDR := api.HDRFacts{
		Formats:         []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		FallbackFormats: []api.HDRFormat{api.HDRFormatHDR10},
		Origin:          api.HDREvidenceMediaInfo,
		Status:          api.HDREvidenceComplete,
	}
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			FileNames:  []string{"Example.Release.2026.mkv"},
			Resolution: "1080p",
			HDR:        targetHDR,
		},
		[]TrackerCandidate{{HDR: targetHDR}},
		trackerspkg.DupePolicy{
			ID:             "ant/duplicate/v3",
			SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionResolution, trackerspkg.DupeDimensionHDR},
		},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0]; got.Relation != api.DupeRelationInsufficientEvidence ||
		got.Reasons[0].Code != "candidate_resolution_missing" {
		t.Fatalf("missing ANT resolution relation = %#v", got)
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

func TestEvaluateSeasonPackContainingExactTargetFileIsExactDuplicate(t *testing.T) {
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
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationExactDuplicate {
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
			reason:    "candidate_content_scope_missing",
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

func TestEvaluateSharedAuxiliaryFileIsNotExactIdentity(t *testing.T) {
	t.Parallel()

	// Same-work releases at different resolutions can ship identically named
	// subtitle/companion files; a shared auxiliary basename must not become
	// exact identity between distinct releases.
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			Names:      []string{"Example.Release.2026.2160p-GRP"},
			Resolution: "2160p",
			FileNames: []string{
				"Example.Release.2026.2160p-GRP/Example.Release.2026.2160p-GRP.mkv",
				"Example.Release.2026.2160p-GRP/2_English.srt",
			},
			SizeBytes: 4000,
		},
		[]TrackerCandidate{{
			Name:       "Example.Release.2026.1080p-GRP",
			Resolution: "1080p",
			Files: []string{
				"Example.Release.2026.1080p-GRP/Example.Release.2026.1080p-GRP.mkv",
				"Example.Release.2026.1080p-GRP/2_English.srt",
			},
			SizeKnown: true,
			SizeBytes: 1500,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := evaluation.Candidates[0].Relation; got == api.DupeRelationExactDuplicate {
		t.Fatalf("shared subtitle basename must not be exact identity, got %q", got)
	}
}

func TestEvaluateSeasonPackProposalNotBlockedBySingleEpisodeFile(t *testing.T) {
	t.Parallel()

	// A proposed season pack shares episode file basenames with existing
	// single-episode uploads; identity must not outrank pack precedence.
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			Names:  []string{"Example.Show.S01.1080p-GRP"},
			Season: 1,
			Pack:   true,
			FileNames: []string{
				"Example.Show.S01.1080p-GRP/Example.Show.S01E01.1080p-GRP.mkv",
				"Example.Show.S01.1080p-GRP/Example.Show.S01E02.1080p-GRP.mkv",
			},
			SizeBytes: 4000,
		},
		[]TrackerCandidate{{
			Name:      "Example.Show.S01E01.1080p-GRP",
			Season:    1,
			Episode:   1,
			Files:     []string{"Example.Show.S01E01.1080p-GRP.mkv"},
			SizeKnown: true,
			SizeBytes: 2000,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationCoexists {
		t.Fatalf("existing episode must be irrelevant to proposed season pack, got %q", got)
	}
	if evaluation.RequiresAction || evaluation.Blocks {
		t.Fatalf("existing episode must not make proposed season pack actionable: %#v", evaluation)
	}
}

func TestEvaluateEqualVideoSetWithConflictingSizeIsNotExact(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{
			Names:     []string{"Example.Release.2026-GRP"},
			FileNames: []string{"Example.Release.2026-GRP.mkv"},
			SizeBytes: 4000,
		},
		[]TrackerCandidate{{
			Name:      "Example.Release.2026.Other-GRP",
			Files:     []string{"Example.Release.2026-GRP.mkv"},
			SizeKnown: true,
			SizeBytes: 1500,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := evaluation.Candidates[0].Relation; got == api.DupeRelationExactDuplicate {
		t.Fatalf("known conflicting sizes must veto file identity, got %q", got)
	}
}

func TestEvaluateGeneralPolicyAppliesPackPrecedence(t *testing.T) {
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
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationExistingPreferred {
		t.Fatalf("compatibility pack relation = %q", got)
	}
}

func TestEvaluateRequiredTargetEvidenceFailsClosed(t *testing.T) {
	t.Parallel()

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{},
		[]TrackerCandidate{{Provider: "Example Provider"}},
		trackerspkg.DupePolicy{SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionProvider}},
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
		ID:               "proposed_webdl_over_webrip",
		Relation:         "proposed_trumps",
		OverridesGeneral: true,
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
	if got := evaluation.Candidates[0].Relation; got != api.DupeRelationInsufficientEvidence {
		t.Fatalf("incomplete compound relation = %q", got)
	}
}

func TestNormalizeCandidateUsesExplicitTitleHDRAsPartialEvidence(t *testing.T) {
	t.Parallel()

	candidate := NormalizeCandidate(api.DupeEntry{Name: "Example.Release.2026.DV.HDR10+.2160p-GRP"}, "ANT")
	if candidate.HDR.Origin != api.HDREvidenceTrackerTitle || candidate.HDR.Status != api.HDREvidencePartial {
		t.Fatalf("ANT title HDR evidence = %#v", candidate.HDR)
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
	if candidate.HDR.Status != api.HDREvidencePartial || candidate.Resolution != "" {
		t.Fatalf("title media evidence = %#v", candidate)
	}

	candidate = NormalizeCandidate(
		api.DupeEntry{Name: "Example.Show.S01E02.1080p-GRP", Season: 1},
		"AITHER",
	)
	if candidate.Episode != 2 {
		t.Fatalf("candidate episode = %d", candidate.Episode)
	}
}

func TestNormalizeCandidatePrefersSpecificTitleMediaKindOverGenericEncodeType(t *testing.T) {
	t.Parallel()

	candidate := NormalizeCandidate(api.DupeEntry{
		Name: "Example.Release.2026.1080p.AMZN.WEBRip.DD+5.1.x265-GRP",
		Type: "x265 Encode",
		Res:  "1080p",
	}, "OE")
	facts := normalizeCandidateFacts(candidate)
	if facts.MediaKind != mediaKindWEBRip || facts.MediaClass != mediaClassEncode || facts.SourceFamily != sourceFamilyWEB {
		t.Fatalf("generic encode overrode title media facts: candidate=%#v facts=%#v", candidate, facts)
	}
}

func TestNormalizeWebDLAndWEBRipKeepDistinctMediaClasses(t *testing.T) {
	t.Parallel()

	webDL := normalizeCandidateFacts(TrackerCandidate{Type: "WEB-DL"})
	webRip := normalizeCandidateFacts(TrackerCandidate{Type: "WEBRip"})
	if webDL.MediaKind != mediaKindWEBDL || webDL.MediaClass != mediaClassWEB || webDL.SourceFamily != sourceFamilyWEB {
		t.Fatalf("WEB-DL facts = %#v", webDL)
	}
	if webRip.MediaKind != mediaKindWEBRip || webRip.MediaClass != mediaClassEncode || webRip.SourceFamily != sourceFamilyWEB {
		t.Fatalf("WEBRip facts = %#v", webRip)
	}

	evaluation := Evaluate(
		api.TrackerDuplicateTarget{Type: "WEB-DL", Resolution: "1080p"},
		[]TrackerCandidate{{Type: "WEBRip", Resolution: "1080p"}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true},
	).Candidates[0]
	if evaluation.Relation != api.DupeRelationCoexists || evaluation.Reasons[0].Code != "media_class_differs" {
		t.Fatalf("WEB-DL versus WEBRip evaluation = %#v", evaluation)
	}
}

func TestEvaluateCanonicalCandidateTypeAndMediaInfoAvoidFalseDifferentType(t *testing.T) {
	t.Parallel()

	candidate := NormalizeCandidate(api.DupeEntry{
		Name:          "Example.Show.S01E12.1080p.WEB-DL-GRP",
		Type:          "WEB-DL",
		CanonicalType: "WEBDL",
		Res:           "1080p",
		HDR: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatSDR},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
	}, "SP")
	target := api.TrackerDuplicateTarget{
		Type:       "WEBDL",
		Resolution: "1080p",
		HDR: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatSDR},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
		Season:  1,
		Episode: 12,
	}
	policy := trackerspkg.DupePolicy{
		ID: "example/duplicate/v2",
		SlotDimensions: []trackerspkg.DupeDimension{
			trackerspkg.DupeDimensionType,
			trackerspkg.DupeDimensionResolution,
			trackerspkg.DupeDimensionHDR,
		},
	}

	result := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
	if result.Relation != api.DupeRelationSameSlot || result.Reasons[0].Code != "same_tracker_slot" {
		t.Fatalf("canonical Unit3D type evaluation = %#v", result)
	}
	if result.Candidate.Type != "WEB-DL" || result.Facts.Type.Value != "WEBDL" {
		t.Fatalf("raw/canonical candidate types = %#v", result)
	}
	if slices.ContainsFunc(result.Findings, func(finding RuleFinding) bool {
		return finding.ReasonCode == "different_type"
	}) {
		t.Fatalf("canonical aliases produced different_type: %#v", result.Findings)
	}
}

func TestNormalizeBareSceneWEBAsWebDL(t *testing.T) {
	t.Parallel()

	candidate := NormalizeCandidate(
		api.DupeEntry{Name: "Example.Release.2026.1080p.WEB.H264-GRP"},
		"AR",
	)
	facts := normalizeCandidateFacts(candidate)
	if facts.MediaKind != mediaKindWEBDL || facts.MediaClass != mediaClassWEB || facts.SourceFamily != sourceFamilyWEB {
		t.Fatalf("bare WEB facts = %#v", facts)
	}

	titleWord := NormalizeCandidate(
		api.DupeEntry{Name: "Example.Web.2026.1080p.BluRay.x264-GRP"},
		"AR",
	)
	titleFacts := normalizeCandidateFacts(titleWord)
	if titleFacts.MediaKind == mediaKindWEBDL || titleFacts.SourceFamily == sourceFamilyWEB {
		t.Fatalf("title word WEB was treated as source marker: %#v", titleFacts)
	}
}

func TestEvaluateTrackerSlotsPrecedeGeneralAndMissingHDR(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{
		ID:             "example/duplicate/v2",
		SlotDimensions: []trackerspkg.DupeDimension{trackerspkg.DupeDimensionResolution, trackerspkg.DupeDimensionHDR},
	}
	target := api.TrackerDuplicateTarget{
		Type:       "WEB-DL",
		Resolution: "1080p",
		HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
	}
	for _, test := range []struct {
		name      string
		candidate TrackerCandidate
		reason    string
	}{
		{
			name: "lower resolution web",
			candidate: TrackerCandidate{
				Type:       "WEB-DL",
				Resolution: "720p",
				HDR:        api.HDRFacts{Origin: api.HDREvidenceUnknown, Status: api.HDREvidenceMissing},
			},
			reason: "different_resolution",
		},
		{
			name: "sd full disc",
			candidate: TrackerCandidate{
				Type:       "DISC",
				Source:     "DVD",
				Resolution: "480i",
				HDR:        api.HDRFacts{Origin: api.HDREvidenceUnknown, Status: api.HDREvidenceMissing},
			},
			reason: "different_resolution",
		},
		{
			name: "same resolution remux",
			candidate: TrackerCandidate{
				Type:       "REMUX",
				Source:     "BluRay",
				Resolution: "1080p",
				HDR:        api.HDRFacts{Origin: api.HDREvidenceUnknown, Status: api.HDREvidenceMissing},
			},
			reason: "media_class_differs",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Evaluate(target, []TrackerCandidate{test.candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
			if got.Relation != api.DupeRelationCoexists || got.Reasons[0].Code != test.reason {
				t.Fatalf("structural relation = %#v", got)
			}
		})
	}
}

func TestEvaluateCompatibilityPolicyUsesGeneralFallback(t *testing.T) {
	t.Parallel()

	policy := trackerspkg.DupePolicy{ID: "ras/duplicate-compat/v1"}
	target := api.TrackerDuplicateTarget{Type: "WEB-DL", Resolution: "1080p"}
	different := Evaluate(
		target,
		[]TrackerCandidate{{Type: "WEB-DL", Resolution: "720p"}},
		policy,
		SearchEvidence{Complete: true},
	).Candidates[0]
	if different.Relation != api.DupeRelationCoexists || different.Reasons[0].Code != "resolution_differs" {
		t.Fatalf("different compatibility slot = %#v", different)
	}
	same := Evaluate(
		target,
		[]TrackerCandidate{{Type: "WEB-DL", Resolution: "1080p"}},
		policy,
		SearchEvidence{Complete: true},
	).Candidates[0]
	if same.Relation != api.DupeRelationSameSlot || same.Reasons[0].Code != "same_tracker_slot" {
		t.Fatalf("same compatibility slot = %#v", same)
	}
}

func TestEvaluateOEPolicyDoesNotInventHDRRequirement(t *testing.T) {
	t.Parallel()

	got := Evaluate(
		api.TrackerDuplicateTarget{
			Type:       "ENCODE",
			Resolution: "1080p",
			HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
		},
		[]TrackerCandidate{{
			Type:       "ENCODE",
			Resolution: "1080p",
			HDR:        api.HDRFacts{Origin: api.HDREvidenceUnknown, Status: api.HDREvidenceMissing},
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true},
	).Candidates[0]
	if got.Relation != api.DupeRelationSameSlot || got.Reasons[0].Code == "candidate_hdr_missing" {
		t.Fatalf("OE-like relation = %#v", got)
	}
}

func TestEvaluateTrackerRuleMissingEvidenceOnlyWhenViable(t *testing.T) {
	t.Parallel()

	rule := trackerspkg.DupeRule{
		ID:               "conditional_cross_resolution",
		EvidenceID:       "synthetic-rule",
		Relation:         "existing_preferred",
		OverridesGeneral: true,
		Conditions: []trackerspkg.DupeCondition{
			{
				Dimension:       trackerspkg.DupeDimensionResolution,
				TargetValues:    []string{"1080p"},
				CandidateValues: []string{"720p"},
			},
			{
				Dimension:        trackerspkg.DupeDimensionProvider,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
		},
	}
	policy := trackerspkg.DupePolicy{ID: "example/duplicate/v2", PrecedenceRules: []trackerspkg.DupeRule{rule}}
	target := api.TrackerDuplicateTarget{Resolution: "1080p", Provider: "Example"}
	candidate := TrackerCandidate{Resolution: "720p"}
	got := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
	if got.Relation != api.DupeRelationInsufficientEvidence || got.WinningRule != rule.ID {
		t.Fatalf("viable missing rule = %#v", got)
	}

	rule.Conditions[0].TargetValues = []string{"2160p"}
	policy.PrecedenceRules = []trackerspkg.DupeRule{rule}
	got = Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true}).Candidates[0]
	if got.Relation != api.DupeRelationCoexists || got.Reasons[0].Code != "resolution_differs" {
		t.Fatalf("disproved missing rule = %#v", got)
	}
}

func TestEvaluateRuleOrderDoesNotChangeResolution(t *testing.T) {
	t.Parallel()

	rules := []trackerspkg.DupeRule{
		{
			ID:         "existing_webdl",
			EvidenceID: "synthetic-rules",
			Relation:   "existing_preferred",
			Conditions: []trackerspkg.DupeCondition{{
				Dimension:       trackerspkg.DupeDimensionType,
				TargetValues:    []string{"WEBRip"},
				CandidateValues: []string{"WEB-DL"},
			}},
		},
		{
			ID:         "unrelated_remux",
			EvidenceID: "synthetic-rules",
			Relation:   "proposed_trumps",
			Conditions: []trackerspkg.DupeCondition{{
				Dimension:       trackerspkg.DupeDimensionType,
				TargetValues:    []string{"REMUX"},
				CandidateValues: []string{"ENCODE"},
			}},
		},
	}
	target := api.TrackerDuplicateTarget{Type: "WEBRip", Resolution: "1080p"}
	candidate := TrackerCandidate{Type: "WEB-DL", Resolution: "1080p"}
	first := Evaluate(
		target,
		[]TrackerCandidate{candidate},
		trackerspkg.DupePolicy{ID: "example/duplicate/v2", PrecedenceRules: rules},
		SearchEvidence{Complete: true},
	).Candidates[0]
	slices.Reverse(rules)
	second := Evaluate(
		target,
		[]TrackerCandidate{candidate},
		trackerspkg.DupePolicy{ID: "example/duplicate/v2", PrecedenceRules: rules},
		SearchEvidence{Complete: true},
	).Candidates[0]
	if first.Relation != api.DupeRelationExistingPreferred || first.Relation != second.Relation ||
		first.WinningRule != second.WinningRule {
		t.Fatalf("order-dependent results: first=%#v second=%#v", first, second)
	}
}

func TestEvaluateEqualSpecificityConflictRequiresManualReview(t *testing.T) {
	t.Parallel()

	condition := trackerspkg.DupeCondition{
		Dimension:       trackerspkg.DupeDimensionType,
		TargetValues:    []string{"WEB-DL"},
		CandidateValues: []string{"WEBRip"},
	}
	policy := trackerspkg.DupePolicy{
		ID: "example/duplicate/v2",
		PrecedenceRules: []trackerspkg.DupeRule{
			{
				ID:         "existing_preferred",
				EvidenceID: "synthetic-rules",
				Relation:   "existing_preferred",
				Conditions: []trackerspkg.DupeCondition{condition},
			},
			{
				ID:         "proposed_preferred",
				EvidenceID: "synthetic-rules",
				Relation:   "proposed_trumps",
				Conditions: []trackerspkg.DupeCondition{condition},
			},
		},
	}
	got := Evaluate(
		api.TrackerDuplicateTarget{Type: "WEB-DL"},
		[]TrackerCandidate{{Type: "WEBRip"}},
		policy,
		SearchEvidence{Complete: true},
	).Candidates[0]
	if got.Relation != api.DupeRelationManualReview || got.Reasons[0].Code != "policy_conflict" ||
		len(got.MatchedRules) < 2 {
		t.Fatalf("conflicting rules = %#v", got)
	}
}

func TestEvaluateExactReleaseNameBlocksDespiteFileMismatch(t *testing.T) {
	t.Parallel()

	got := Evaluate(
		api.TrackerDuplicateTarget{
			Names:     []string{"Example.Release.2026.1080p-GRP"},
			FileNames: []string{"Example.Release.2026.mkv"},
		},
		[]TrackerCandidate{{
			Name:      "Example.Release.2026.1080p-GRP",
			Files:     []string{"Different.File.mkv"},
			FileCount: 1,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true},
	).Candidates[0]
	if got.Relation != api.DupeRelationExactDuplicate {
		t.Fatalf("exact release name relation = %#v", got)
	}
}

func TestEvaluatePartialCandidateFileSetWithExactBasenameIsExact(t *testing.T) {
	t.Parallel()

	got := Evaluate(
		api.TrackerDuplicateTarget{FileNames: []string{"Example.Release.2026.mkv"}},
		[]TrackerCandidate{{
			Files:     []string{"Example.Release.2026.mkv"},
			FileCount: 2,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true},
	).Candidates[0]
	if got.Relation != api.DupeRelationExactDuplicate {
		t.Fatalf("partial file set relation = %#v", got)
	}
}

func TestEvaluateGeneralFallbackPreservesPotentialCandidates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate TrackerCandidate
		want      api.DupeRelation
	}{
		{
			name: "year difference",
			target: api.TrackerDuplicateTarget{
				Names:      []string{"Example.Release.2025.1080p.WEB-DL-GRP"},
				Type:       "WEB-DL",
				Resolution: "1080p",
			},
			candidate: TrackerCandidate{
				Name:       "Example.Release.2026.1080p.WEB-DL-GRP",
				Type:       "WEB-DL",
				Resolution: "1080p",
			},
			want: api.DupeRelationSameSlot,
		},
		{
			name: "restyled name",
			target: api.TrackerDuplicateTarget{
				Names:      []string{"Example.Release.2026.1080p.WEB-DL-GRP"},
				Type:       "WEB-DL",
				Resolution: "1080p",
			},
			candidate: TrackerCandidate{
				Name:       "Restyled Alias - GRP",
				Type:       "WEB-DL",
				Resolution: "1080p",
			},
			want: api.DupeRelationSameSlot,
		},
		{
			name:      "fuzzy filename",
			target:    api.TrackerDuplicateTarget{FileNames: []string{"Example.Release.2026.mkv"}},
			candidate: TrackerCandidate{Files: []string{"Example.Release.2026.PROPER.mkv"}},
			want:      api.DupeRelationSameSlot,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Evaluate(test.target, []TrackerCandidate{test.candidate}, trackerspkg.DupePolicy{}, SearchEvidence{}).Candidates[0]
			if got.Relation != test.want {
				t.Fatalf("relation = %q, want %q", got.Relation, test.want)
			}
		})
	}
}

func TestEvaluateGeneralHDRRequiresPositiveEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    api.TrackerDuplicateTarget
		candidate TrackerCandidate
		want      api.DupeRelation
	}{
		{
			name: "2160p SDR and HDR coexist",
			target: api.TrackerDuplicateTarget{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
			},
			candidate: TrackerCandidate{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "2160p proposed DV HDR trumps HDR",
			target: api.TrackerDuplicateTarget{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			},
			candidate: TrackerCandidate{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10Plus),
			},
			want: api.DupeRelationProposedTrumps,
		},
		{
			name: "2160p existing DV HDR trumps HDR",
			target: api.TrackerDuplicateTarget{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			},
			candidate: TrackerCandidate{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus),
			},
			want: api.DupeRelationExistingPreferred,
		},
		{
			name: "distinct media class precedes DV HDR trumping",
			target: api.TrackerDuplicateTarget{
				Type:       "WEB-DL",
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			},
			candidate: TrackerCandidate{
				Type:       "REMUX",
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "2160p DV and DV HDR coexist",
			target: api.TrackerDuplicateTarget{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision),
			},
			candidate: TrackerCandidate{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatDolbyVision, api.HDRFormatHDR10),
			},
			want: api.DupeRelationCoexists,
		},
		{
			name: "1080p ignores HDR",
			target: api.TrackerDuplicateTarget{
				Resolution: "1080p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
			},
			candidate: TrackerCandidate{
				Resolution: "1080p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			},
			want: api.DupeRelationSameSlot,
		},
		{
			name: "other resolutions ignore HDR",
			target: api.TrackerDuplicateTarget{
				Resolution: "720p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
			},
			candidate: TrackerCandidate{
				Resolution: "720p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			},
			want: api.DupeRelationSameSlot,
		},
		{
			name: "partial 2160p resolution cannot prove coexistence",
			target: api.TrackerDuplicateTarget{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
			},
			candidate: TrackerCandidate{
				Name: "Example.Release.2026.2160p.HDR-GRP",
				HDR:  testHDR(api.HDREvidenceComplete, api.HDRFormatHDR10),
			},
			want: api.DupeRelationSameSlot,
		},
		{
			name: "partial HDR cannot prove coexistence",
			target: api.TrackerDuplicateTarget{
				Resolution: "2160p",
				HDR:        testHDR(api.HDREvidenceComplete, api.HDRFormatSDR),
			},
			candidate: TrackerCandidate{
				Resolution: "2160p",
				HDR: api.HDRFacts{
					Formats: []api.HDRFormat{api.HDRFormatHDR10},
					Origin:  api.HDREvidenceTrackerTitle,
					Status:  api.HDREvidencePartial,
				},
			},
			want: api.DupeRelationSameSlot,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Evaluate(
				test.target,
				[]TrackerCandidate{test.candidate},
				trackerspkg.DupePolicy{},
				SearchEvidence{Complete: true},
			).Candidates[0]
			if got.Relation != test.want {
				t.Fatalf("general HDR relation = %q, want %q", got.Relation, test.want)
			}
		})
	}
}

func TestEvaluateGeneralSeasonPackContainmentIsDirectional(t *testing.T) {
	t.Parallel()

	proposedPack := Evaluate(
		api.TrackerDuplicateTarget{Season: 1, Pack: true},
		[]TrackerCandidate{{Season: 1, Episode: 2}},
		trackerspkg.DupePolicy{},
		SearchEvidence{WorkScope: WorkScopeProviderID},
	).Candidates[0]
	if proposedPack.Relation != api.DupeRelationCoexists {
		t.Fatalf("proposed pack relation = %#v", proposedPack)
	}

	existingPack := Evaluate(
		api.TrackerDuplicateTarget{Season: 1, Episode: 2},
		[]TrackerCandidate{{Season: 1, Pack: true}},
		trackerspkg.DupePolicy{},
		SearchEvidence{WorkScope: WorkScopeProviderID},
	).Candidates[0]
	if existingPack.Relation != api.DupeRelationExistingPreferred {
		t.Fatalf("existing pack relation = %#v", existingPack)
	}
}

func TestEvaluateGenericVideoBasenameDoesNotEstablishIdentity(t *testing.T) {
	t.Parallel()

	// Generic member names repeat across distinct releases. A shared generic
	// video basename must not establish identity when the video sets differ,
	// and identical generic single-file sets with conflicting known sizes
	// must fail the size check.
	sharedSample := Evaluate(
		api.TrackerDuplicateTarget{
			Names:     []string{"Example.Release.2026.2160p-GRP"},
			FileNames: []string{"Example.Release.2026.2160p-GRP.mkv", "sample.mkv"},
			SizeBytes: 4000,
		},
		[]TrackerCandidate{{
			Name:      "Example.Release.2026.1080p-OTHER",
			Files:     []string{"Example.Release.2026.1080p-OTHER.mkv", "sample.mkv"},
			SizeKnown: true,
			SizeBytes: 1500,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := sharedSample.Candidates[0].Relation; got == api.DupeRelationExactDuplicate {
		t.Fatalf("shared generic sample basename must not be exact identity, got %q", got)
	}

	genericSingle := Evaluate(
		api.TrackerDuplicateTarget{
			Names:     []string{"Example.Release.2026.2160p-GRP"},
			FileNames: []string{"01.mkv"},
			SizeBytes: 4000,
		},
		[]TrackerCandidate{{
			Name:      "Example.Release.2026.1080p-OTHER",
			Files:     []string{"01.mkv"},
			SizeKnown: true,
			SizeBytes: 1500,
		}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := genericSingle.Candidates[0].Relation; got == api.DupeRelationExactDuplicate {
		t.Fatalf("generic single-file basename with conflicting sizes must not be exact, got %q", got)
	}
}

func TestEvaluateSingleAuxiliaryFileStemDoesNotEstablishIdentity(t *testing.T) {
	t.Parallel()

	// A lone non-video file whose stem coincides with the candidate's release
	// name must not establish identity through the single-file stem fallback.
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{FileNames: []string{"Example.Release.2026.nfo"}},
		[]TrackerCandidate{{Name: "Example.Release.2026"}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	if got := evaluation.Candidates[0].Relation; got == api.DupeRelationExactDuplicate {
		t.Fatalf("auxiliary stem fallback must not be exact identity, got %q", got)
	}
}

func TestEvaluatePackContainmentRequiresAuthoritativeWorkScope(t *testing.T) {
	t.Parallel()

	// A title-fallback search cannot prove candidates belong to the same
	// work, so a season-number coincidence with a different show's pack must
	// not block the proposal.
	evaluation := Evaluate(
		api.TrackerDuplicateTarget{Season: 1, Episode: 2},
		[]TrackerCandidate{{Season: 1, Pack: true}},
		trackerspkg.DupePolicy{},
		SearchEvidence{Complete: true, WorkScope: WorkScopeTitle},
	)
	if got := evaluation.Candidates[0].Relation; got == api.DupeRelationExistingPreferred {
		t.Fatalf("title-fallback pack containment must not block, got %q", got)
	}
	if evaluation.Blocks {
		t.Fatalf("title-fallback season coincidence must not set Blocks")
	}
}

func TestNormalizeFactsPreservesTitleProvenanceAndContradictions(t *testing.T) {
	t.Parallel()

	titleOnly := normalizeCandidateFacts(TrackerCandidate{Name: "Example.Release.2026.2160p-GRP"})
	if titleOnly.Resolution.Status != FactPartial || titleOnly.Resolution.Origin != FactOriginTrackerTitle {
		t.Fatalf("title-only resolution = %#v", titleOnly.Resolution)
	}
	contradictory := normalizeCandidateFacts(TrackerCandidate{
		Name:       "Example.Release.2026.2160p-GRP",
		Resolution: "1080p",
	})
	if contradictory.Resolution.Status != FactContradictory ||
		!slices.Contains(contradictory.Resolution.Contradictions, "2160p") {
		t.Fatalf("contradictory resolution = %#v", contradictory.Resolution)
	}
	hdrContradictory := normalizeCandidateFacts(TrackerCandidate{
		Name: "Example.Release.2026.HDR.2160p-GRP",
		HDR: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatHDR10Plus},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
	})
	if hdrContradictory.HDR.Status != api.HDREvidenceContradictory ||
		hdrContradictory.HDR.Origin != api.HDREvidenceMediaInfo ||
		!slices.Equal(hdrContradictory.HDR.Formats, []api.HDRFormat{api.HDRFormatHDR10Plus}) {
		t.Fatalf("contradictory HDR = %#v", hdrContradictory.HDR)
	}
}

func TestNormalizeContentScopeRanges(t *testing.T) {
	t.Parallel()

	target := normalizeTargetFacts(api.TrackerDuplicateTarget{Names: []string{"Example.Show.S01E01-E03.1080p-GRP"}})
	overlap := normalizeCandidateFacts(TrackerCandidate{Name: "Example.Show.S01E03.1080p-GRP"})
	disjoint := normalizeCandidateFacts(TrackerCandidate{Name: "Example.Show.S01E04-E06.1080p-GRP"})
	if got := compareContentScopes(target.Content, overlap.Content); got != contentOverlaps {
		t.Fatalf("overlap = %q", got)
	}
	if got := compareContentScopes(target.Content, disjoint.Content); got != contentDefinitelyDisjoint {
		t.Fatalf("disjoint = %q", got)
	}

	for _, test := range []struct {
		name      string
		target    contentScope
		candidate contentScope
		want      contentOverlap
	}{
		{
			name:      "different seasons",
			target:    normalizeTargetFacts(api.TrackerDuplicateTarget{Season: 1, Episode: 1}).Content,
			candidate: normalizeCandidateFacts(TrackerCandidate{Season: 2, Episode: 1}).Content,
			want:      contentDefinitelyDisjoint,
		},
		{
			name:      "season pack contains episode",
			target:    normalizeTargetFacts(api.TrackerDuplicateTarget{Season: 1, Pack: true}).Content,
			candidate: normalizeCandidateFacts(TrackerCandidate{Season: 1, Episode: 2}).Content,
			want:      contentOverlaps,
		},
		{
			name:      "complete series contains season",
			target:    normalizeTargetFacts(api.TrackerDuplicateTarget{Names: []string{"Example.Show.Complete.Series-GRP"}, Pack: true}).Content,
			candidate: normalizeCandidateFacts(TrackerCandidate{Season: 2, Pack: true}).Content,
			want:      contentOverlaps,
		},
		{
			name:      "unknown pack range",
			target:    normalizeTargetFacts(api.TrackerDuplicateTarget{Pack: true}).Content,
			candidate: normalizeCandidateFacts(TrackerCandidate{Season: 2, Episode: 1}).Content,
			want:      contentUnknown,
		},
		{
			name:      "special versus regular season",
			target:    normalizeTargetFacts(api.TrackerDuplicateTarget{Season: 0, Episode: 1}).Content,
			candidate: normalizeCandidateFacts(TrackerCandidate{Season: 1, Episode: 1}).Content,
			want:      contentDefinitelyDisjoint,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := compareContentScopes(test.target, test.candidate); got != test.want {
				t.Fatalf("content overlap = %q, want %q", got, test.want)
			}
		})
	}
}

func testHDR(status api.HDREvidenceStatus, formats ...api.HDRFormat) api.HDRFacts {
	return api.HDRFacts{
		Formats: append([]api.HDRFormat(nil), formats...),
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  status,
	}
}
