// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// conditionalSlot resolves only declared alternatives with complete predicates.
// Conflicting matched slot IDs remain unknown rather than depending on table order.
func conditionalSlot(facts normalizedFacts, slots []trackerspkg.DupeSlot) (string, []string) {
	var id string
	var reviews []string
	for _, slot := range slots {
		result, _ := evaluateSetPredicates(facts, facts, slot.Predicates, "release")
		if result != setPredicateMatched {
			continue
		}
		if id != "" && id != slot.ID {
			return "", nil
		}
		id = slot.ID
		if slot.ReviewReason != "" && !slices.Contains(reviews, slot.ReviewReason) {
			reviews = append(reviews, slot.ReviewReason)
		}
	}
	return id, reviews
}

func targetReviewReasons(facts normalizedFacts, policy trackerspkg.DupePolicy) []api.DupeReason {
	var reasons []api.DupeReason
	if len(policy.Slots) > 0 {
		id, reviews := conditionalSlot(facts, policy.Slots)
		if id == "" {
			reasons = append(reasons, api.DupeReason{
				Code:    "ordinary_slot_unproven",
				Message: "The release does not establish a listed ordinary slot. Review incomplete technical evidence or the required tracker justification and staff approval.",
			})
		}
		for _, review := range reviews {
			reasons = append(reasons, api.DupeReason{Code: "slot_source_review", Message: review})
		}
	}
	for _, rule := range policy.TargetReviewRules {
		result, _ := evaluateSetPredicates(facts, facts, rule.Predicates, "target")
		if result == setPredicateMatched || rule.IncludeIndeterminate && result == setPredicateIndeterminate {
			reasons = append(reasons, api.DupeReason{Code: rule.ID, Message: rule.Reason})
		}
	}
	return reasons
}

func collectConditionalSlotFinding(target normalizedFacts, candidate normalizedFacts, policy trackerspkg.DupePolicy) RuleFinding {
	finding := RuleFinding{
		RuleID:     policy.ID + "/ordinary_slot",
		EvidenceID: policy.EvidenceID,
		Source:     "tracker",
		Status:     RuleFindingMatched,
		Relation:   api.DupeRelationSameSlot,
		ReasonCode: "same_tracker_slot",
		Priority:   findingPriorityFallback,
	}
	for _, facts := range []normalizedFacts{target, candidate} {
		for _, dimension := range []trackerspkg.DupeDimension{
			trackerspkg.DupeDimensionType, trackerspkg.DupeDimensionSource, trackerspkg.DupeDimensionResolution,
			trackerspkg.DupeDimensionCodec, trackerspkg.DupeDimensionHDR,
		} {
			if setDimensionFact(facts, dimension).Status == FactContradictory {
				finding.Status = RuleFindingIndeterminate
				finding.Relation = api.DupeRelationManualReview
				finding.ReasonCode = "ordinary_slot_contradictory"
				finding.Contradictions = append(finding.Contradictions, string(dimension))
				finding.Priority = findingPriorityContradiction
			}
		}
	}
	if len(finding.Contradictions) > 0 {
		return finding
	}
	targetID, _ := conditionalSlot(target, policy.Slots)
	candidateID, _ := conditionalSlot(candidate, policy.Slots)
	switch {
	case targetID == "" || candidateID == "":
		finding.Status = RuleFindingIndeterminate
		finding.Relation = api.DupeRelationInsufficientEvidence
		finding.ReasonCode = "ordinary_slot_unproven"
		finding.Missing = []string{"ordinary_slot"}
		finding.Priority = findingPrioritySlotMissing
	case targetID != candidateID:
		finding.Relation = api.DupeRelationCoexists
		finding.ReasonCode = "distinct_ordinary_slot"
		finding.Priority = findingPriorityTrackerMatched
	}
	return finding
}
