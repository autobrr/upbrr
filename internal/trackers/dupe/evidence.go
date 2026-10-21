// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func candidateEvidenceFingerprint(candidate TrackerCandidate) api.WorkflowFingerprint {
	candidate.DetailsLink = ""
	fingerprint, _ := api.CanonicalWorkflowFingerprint(candidate)
	return fingerprint
}

func appendEvidenceFinding(target api.TrackerDuplicateTarget, targetFacts normalizedFacts, candidate TrackerCandidate, facts normalizedFacts,
	policy trackers.DupePolicy, workScope WorkScope, findings []RuleFinding) []RuleFinding {
	if policy.CompareEvidence == nil {
		return findings
	}
	ordinary := resolveCandidateFindings(candidate, facts, findings)
	for _, finding := range findings {
		if finding.Status != RuleFindingMatched {
			continue
		}
		if finding.Priority >= findingPriorityDisjointContent {
			return findings
		}
		if finding.Relation == api.DupeRelationCoexists && finding.ReasonCode != "proposed_season_pack" &&
			finding.ReasonCode != "distinct_hdr_slot" {
			return findings
		}
	}
	// Reviewed source evidence cannot repair contradictory normalized slot facts.
	for _, normalized := range []normalizedFacts{targetFacts, facts} {
		for _, fact := range []Fact{normalized.Type, normalized.Source, normalized.Resolution, normalized.Codec,
			normalized.Edition, normalized.Region, normalized.ThreeD, normalized.Group} {
			if fact.Status == FactContradictory {
				return findings
			}
		}
	}
	slot, _ := collectTrackerSlotFinding(target, targetFacts, candidate, facts, policy)
	if len(slot.Contradictions) > 0 {
		return findings
	}
	slotComplete := len(slot.Missing) == 0
	for _, comparison := range slot.comparisons {
		if comparison.Result == DimensionUnknown || comparison.Result == DimensionNotApplicable {
			slotComplete = false
		}
	}

	proposed := target
	proposed.Type = string(targetFacts.MediaKind)
	proposed.Resolution, proposed.HDR = targetFacts.Resolution.Value, targetFacts.HDR
	existing := api.TrackerDuplicateTarget{
		Evidence:   candidate.Evidence,
		Type:       string(facts.MediaKind),
		Resolution: facts.Resolution.Value,
		HDR:        facts.HDR,
		Group:      candidate.Group,
		Repack:     candidate.Repack,
		Edition:    facts.Edition.Value,
		Region:     facts.Region.Value,
		ThreeD:     facts.ThreeD.Value,
	}
	if candidate.Internal {
		existing.Evidence.InternalStatus = "internal"
	}
	decision := policy.CompareEvidence(api.DupeEvidenceContext{
		SlotEvidenceComplete: slotComplete && (workScope == WorkScopeProviderID || workScope == WorkScopeTrackerGroup) &&
			sameKnownContentScope(
				targetFacts.Content,
				facts.Content,
			) && targetFacts.Resolution.Status == FactComplete && facts.Resolution.Status == FactComplete &&
			dimensionFact(
				targetFacts,
				trackers.DupeDimensionMediaKind,
			).Status == FactComplete && dimensionFact(facts, trackers.DupeDimensionMediaKind).Status == FactComplete &&
			targetFacts.HDR.Status == api.HDREvidenceComplete && facts.HDR.Status == api.HDREvidenceComplete,
		Target:               proposed,
		Candidate:            existing,
		CandidateID:          candidate.ID,
		CandidateFingerprint: candidateEvidenceFingerprint(candidate),
		Trumpable:            candidate.Trumpable,
		Relation:             ordinary.Relation,
		Reason:               ordinary.Reasons[0].Code,
	})
	if decision.Relation == "" || decision.Relation == api.DupeRelationProposedTrumps && !slotComplete {
		return findings
	}
	return append(findings, RuleFinding{
		RuleID:     policy.ID + "/source_evidence",
		EvidenceID: policy.EvidenceID,
		Source:     "tracker",
		Status:     RuleFindingMatched,
		Relation:   decision.Relation,
		ReasonCode: decision.Reason,
		Priority:   findingPriorityDisjointContent - 1,
	})
}
