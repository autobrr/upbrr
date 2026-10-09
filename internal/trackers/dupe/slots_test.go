// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"testing"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestConditionalSlotsRequireCompleteEvidence(t *testing.T) {
	t.Parallel()
	policy := trackerspkg.DupePolicy{ID: "example/duplicate/v1", Slots: []trackerspkg.DupeSlot{{
		ID: "1080p-encode", Predicates: []trackerspkg.DupeSetPredicate{
			{
				Dimension:        trackerspkg.DupeDimensionMediaKind,
				Values:           []string{"disc_encode"},
				RequiresComplete: true,
			},
			{
				Dimension:        trackerspkg.DupeDimensionResolution,
				Values:           []string{"1080p"},
				RequiresComplete: true,
			},
		},
	}}}
	target := testSetTarget()
	complete := Evaluate(target, nil, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if complete.RequiresAction || len(complete.ReviewReasons) != 0 {
		t.Fatalf("complete ordinary slot requires review: %#v", complete)
	}
	target.Type = ""
	target.Source = ""
	target.VideoEncode = ""
	target.Names = []string{"Example.Film.2026.1080p.BluRay.x264-GRP"}
	partial := Evaluate(target, nil, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if !partial.RequiresAction || len(partial.ReviewReasons) == 0 {
		t.Fatalf("title-only media classification established a slot: %#v", partial)
	}
}

func TestConditionalSlotsRetainExternalReviewOnEmptySearch(t *testing.T) {
	t.Parallel()
	policy := trackerspkg.DupePolicy{ID: "example/duplicate/v1", Slots: []trackerspkg.DupeSlot{{
		ID:           "conditional",
		ReviewReason: "Confirm source availability with tracker staff.",
		Predicates: []trackerspkg.DupeSetPredicate{{
			Dimension:        trackerspkg.DupeDimensionResolution,
			Values:           []string{"1080p"},
			RequiresComplete: true,
		}},
	}}}
	got := Evaluate(testSetTarget(), nil, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if !got.RequiresAction || got.Blocks || len(got.ReviewReasons) != 1 {
		t.Fatalf("source review vanished from empty search: %#v", got)
	}
}

func TestConditionalSlotOrderCannotResolveAmbiguity(t *testing.T) {
	t.Parallel()
	predicates := []trackerspkg.DupeSetPredicate{{
		Dimension:        trackerspkg.DupeDimensionResolution,
		Values:           []string{"1080p"},
		RequiresComplete: true,
	}}
	slots := []trackerspkg.DupeSlot{{ID: "one", Predicates: predicates}, {ID: "two", Predicates: predicates}}
	facts := normalizeTargetFacts(testSetTarget())
	for range 2 {
		if id, _ := conditionalSlot(facts, slots); id != "" {
			t.Fatalf("ambiguous table returned %q", id)
		}
		slots[0], slots[1] = slots[1], slots[0]
	}
}

func TestOptionalDirectionalVariantNeedsCompleteEvidence(t *testing.T) {
	t.Parallel()
	target := testSetTarget()
	candidate := testSetCandidate("one", 80)
	policy := trackerspkg.DupePolicy{ID: "example/duplicate/v1", PrecedenceRules: []trackerspkg.DupeRule{{
		ID:       "example-precedence",
		Relation: string(api.DupeRelationProposedTrumps),
		Conditions: []trackerspkg.DupeCondition{{
			Dimension:        trackerspkg.DupeDimensionEdition,
			ValuesEqual:      true,
			RequiresComplete: true,
			Optional:         true,
		}},
	}}}
	got := Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation != api.DupeRelationProposedTrumps {
		t.Fatalf("absent default editions prevented comparison: %#v", got)
	}
	candidate.Edition = "extended"
	got = Evaluate(target, []TrackerCandidate{candidate}, policy, SearchEvidence{Complete: true, WorkScope: WorkScopeProviderID})
	if got.Candidates[0].Relation == api.DupeRelationProposedTrumps {
		t.Fatalf("one-sided edition granted precedence: %#v", got)
	}
}

func TestConflictingPreparedCodecsRemainContradictory(t *testing.T) {
	t.Parallel()
	target := testSetTarget()
	target.VideoCodec = "HEVC"
	if got := normalizeTargetFacts(target).Codec; got.Status != FactContradictory {
		t.Fatalf("conflicting codec and encoder = %#v", got)
	}
	target.VideoCodec = "AVC"
	if got := normalizeTargetFacts(target).Codec; got.Status != FactComplete {
		t.Fatalf("equivalent codec and encoder = %#v", got)
	}
}
