// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"strings"
	"testing"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEvaluateSetCapacity(t *testing.T) {
	t.Parallel()

	policy := testSetPolicy()
	target := testSetTarget()
	tests := []struct {
		name            string
		candidates      []TrackerCandidate
		wantRelation    api.DupeRelation
		wantSetRelation api.DupeRelation
		wantAction      bool
	}{
		{name: "zero", wantSetRelation: api.DupeRelationCoexists},
		{
			name:            "exact threshold",
			candidates:      []TrackerCandidate{testSetCandidate("one", 80)},
			wantRelation:    api.DupeRelationCoexists,
			wantSetRelation: api.DupeRelationCoexists,
		},
		{
			name:            "just below threshold",
			candidates:      []TrackerCandidate{testSetCandidate("one", 81)},
			wantRelation:    api.DupeRelationManualReview,
			wantSetRelation: api.DupeRelationManualReview,
			wantAction:      true,
		},
		{
			name: "unknown size",
			candidates: []TrackerCandidate{{
				ID:         "one",
				Type:       "ENCODE",
				Source:     "BluRay",
				Resolution: "1080p",
				Codec:      "x264",
				HDR:        testSetSDR(),
			}},
			wantRelation:    api.DupeRelationInsufficientEvidence,
			wantSetRelation: api.DupeRelationInsufficientEvidence,
			wantAction:      true,
		},
		{
			name: "malformed candidate",
			candidates: []TrackerCandidate{{
				ID:         "one",
				Type:       "ENCODE",
				Source:     "BluRay",
				Resolution: "1080p",
				SizeBytes:  80,
				SizeKnown:  true,
				HDR:        testSetSDR(),
			}},
			wantRelation:    api.DupeRelationInsufficientEvidence,
			wantSetRelation: api.DupeRelationInsufficientEvidence,
			wantAction:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := Evaluate(target, test.candidates, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
			if len(got.SetFindings) != 1 || got.SetFindings[0].Relation != test.wantSetRelation || got.RequiresAction != test.wantAction {
				t.Fatalf("set evaluation = %#v", got)
			}
			if len(got.Candidates) > 0 && got.Candidates[0].Relation != test.wantRelation {
				t.Fatalf("candidate relation = %#v, want %s", got.Candidates[0], test.wantRelation)
			}
		})
	}
}

func TestEvaluateSetCapacityUsesWholeOrderIndependentCollection(t *testing.T) {
	t.Parallel()

	policy := testSetPolicy()
	target := testSetTarget()
	first := testSetCandidate("one", 80)
	second := testSetCandidate("two", 60)
	for _, candidate := range []TrackerCandidate{first, second} {
		got := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
		if got.Candidates[0].Relation != api.DupeRelationCoexists {
			t.Fatalf("single candidate should fit second slot: %#v", got)
		}
	}

	forward := Evaluate(target, []TrackerCandidate{first, second}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	reverse := Evaluate(target, []TrackerCandidate{second, first}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	for _, got := range []Evaluation{forward, reverse} {
		if len(got.SetFindings) != 1 || got.SetFindings[0].ReasonCode != "set_capacity_full" || !got.RequiresAction {
			t.Fatalf("full set evaluation = %#v", got)
		}
		for _, candidate := range got.Candidates {
			if candidate.Relation != api.DupeRelationManualReview {
				t.Fatalf("full set candidate = %#v", candidate)
			}
		}
	}
	if !slices.Equal(forward.SetFindings[0].CandidateIDs, reverse.SetFindings[0].CandidateIDs) ||
		!slices.Equal(forward.SetFindings[0].FactSummaries, reverse.SetFindings[0].FactSummaries) {
		t.Fatalf("set finding changed with order: forward=%#v reverse=%#v", forward.SetFindings[0], reverse.SetFindings[0])
	}

	third := testSetCandidate("three", 40)
	got := Evaluate(target, []TrackerCandidate{first, second, third}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.SetFindings[0].ExistingOccupancy != 3 || got.SetFindings[0].Relation != api.DupeRelationManualReview {
		t.Fatalf("three-candidate set = %#v", got.SetFindings[0])
	}
}

func TestEvaluateSetCapacityFailsClosedWithoutCompleteSearch(t *testing.T) {
	t.Parallel()

	got := Evaluate(
		testSetTarget(),
		[]TrackerCandidate{testSetCandidate("one", 80)},
		testSetPolicy(),
		SearchEvidence{Complete: false},
	)
	if got.SetFindings[0].Relation != api.DupeRelationInsufficientEvidence ||
		!slices.Contains(got.SetFindings[0].Missing, "search_complete") || !got.RequiresAction {
		t.Fatalf("incomplete set = %#v", got.SetFindings[0])
	}
}

func TestEvaluateSetCapacityNeverInfersQualityFromSize(t *testing.T) {
	t.Parallel()

	got := Evaluate(
		testSetTarget(),
		[]TrackerCandidate{testSetCandidate("one", 80)},
		testSetPolicy(),
		SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID},
	)
	finding := got.SetFindings[0]
	combined := finding.ReasonCode + " " + dupeReasonMessage(finding.ReasonCode)
	if strings.Contains(strings.ToLower(combined), "quality") ||
		strings.Contains(strings.ToLower(combined), "larger") || strings.Contains(strings.ToLower(combined), "higher") {
		t.Fatalf("size inferred a quality role: %#v", finding)
	}
}

func testSetPolicy() trackerspkg.DupePolicy {
	encodeKinds := []string{"disc_encode", "other_encode"}
	return trackerspkg.DupePolicy{
		ID:         "example/duplicate/v2",
		EvidenceID: "example-rules",
		SlotDimensions: []trackerspkg.DupeDimension{
			trackerspkg.DupeDimensionMediaKind,
			trackerspkg.DupeDimensionResolution,
			trackerspkg.DupeDimensionHDR,
		},
		SetRules: []trackerspkg.DupeSetRule{{
			ID:         "example/duplicate/v2/encode_capacity",
			EvidenceID: "example-rules",
			TargetPredicates: []trackerspkg.DupeSetPredicate{
				{
					Dimension:        trackerspkg.DupeDimensionMediaKind,
					Values:           encodeKinds,
					RequiresComplete: true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionResolution,
					Values:           []string{"1080p"},
					RequiresComplete: true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionCodec,
					Values:           []string{"h264"},
					RequiresComplete: true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionHDR,
					Values:           []string{"sdr"},
					RequiresComplete: true,
				},
			},
			CandidatePredicates: []trackerspkg.DupeSetPredicate{
				{
					Dimension:        trackerspkg.DupeDimensionMediaKind,
					Values:           encodeKinds,
					RequiresComplete: true,
					MatchTarget:      true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionResolution,
					Values:           []string{"1080p"},
					RequiresComplete: true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionCodec,
					Values:           []string{"h264"},
					RequiresComplete: true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionHDR,
					Values:           []string{"sdr"},
					RequiresComplete: true,
				},
				{
					Dimension:        trackerspkg.DupeDimensionEdition,
					RequiresComplete: true,
					MatchTarget:      true,
					Optional:         true,
				},
			},
			Capacity:                     2,
			MinimumSizeSeparationPercent: 20,
		}},
	}
}

func testSetTarget() api.TrackerDuplicateTarget {
	return api.TrackerDuplicateTarget{
		Type:        "ENCODE",
		Source:      "BluRay",
		Resolution:  "1080p",
		VideoEncode: "x264",
		SizeBytes:   100,
		HDR:         testSetSDR(),
	}
}

func testSetCandidate(id string, size int64) TrackerCandidate {
	return TrackerCandidate{
		ID:         id,
		Type:       "ENCODE",
		Source:     "BluRay",
		Resolution: "1080p",
		Codec:      "x264",
		SizeBytes:  size,
		SizeKnown:  true,
		HDR:        testSetSDR(),
	}
}

func testSetSDR() api.HDRFacts {
	return api.HDRFacts{
		Formats: []api.HDRFormat{api.HDRFormatSDR},
		Origin:  api.HDREvidenceTrackerAPI,
		Status:  api.HDREvidenceComplete,
	}
}
