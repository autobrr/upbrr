// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const findingPrioritySet = findingPriorityTrackerMatched + 5

// SetFinding retains one collection-level capacity decision without raw
// tracker payloads.
type SetFinding struct {
	RuleID                           string
	EvidenceID                       string
	Status                           RuleFindingStatus
	Relation                         api.DupeRelation
	ReasonCode                       string
	ExistingOccupancy                int
	Capacity                         int
	MinimumSizeSeparationPercent     float64
	ObservedMinimumSeparationPercent float64
	SeparationKnown                  bool
	CandidateIDs                     []string
	FactSummaries                    []string
	Missing                          []string
	affectedCandidateIndexes         []int
}

type setPredicateResult uint8

const (
	setPredicateMatched setPredicateResult = iota
	setPredicateMismatch
	setPredicateIndeterminate
)

func evaluateSetRules(
	targetFacts normalizedFacts,
	candidates []TrackerCandidate,
	candidateFacts []normalizedFacts,
	policy trackerspkg.DupePolicy,
	search SearchEvidence,
) []SetFinding {
	findings := make([]SetFinding, 0, len(policy.SetRules))
	for _, rule := range policy.SetRules {
		targetResult, targetMissing := evaluateSetPredicates(targetFacts, targetFacts, rule.TargetPredicates, "target")
		if targetResult == setPredicateMismatch {
			continue
		}
		finding := SetFinding{
			RuleID:                       strings.TrimSpace(rule.ID),
			EvidenceID:                   firstNonEmpty(rule.EvidenceID, policy.EvidenceID),
			Capacity:                     rule.Capacity,
			MinimumSizeSeparationPercent: rule.MinimumSizeSeparationPercent,
			FactSummaries:                []string{setFactSummary("target", "", targetFacts)},
		}
		if targetResult == setPredicateIndeterminate {
			used := make([]int, 0, len(candidates))
			for index := range candidates {
				result, missing := evaluateSetPredicates(targetFacts, candidateFacts[index], rule.CandidatePredicates, "candidate")
				if result == setPredicateMismatch {
					continue
				}
				finding.affectedCandidateIndexes = appendUniqueIndex(finding.affectedCandidateIndexes, index)
				used = appendUniqueIndex(used, index)
				if result == setPredicateIndeterminate {
					finding.Missing = append(finding.Missing, setCandidateMissing(candidates[index].ID, missing)...)
				}
			}
			if len(finding.affectedCandidateIndexes) == 0 {
				continue
			}
			finding.addCandidateEvidence(candidates, candidateFacts, used)
			finding.setMissing(append(finding.Missing, targetMissing...))
			findings = append(findings, finding)
			continue
		}

		members := make([]int, 0, len(candidates))
		used := make([]int, 0, len(candidates))
		for index := range candidates {
			result, missing := evaluateSetPredicates(targetFacts, candidateFacts[index], rule.CandidatePredicates, "candidate")
			switch result {
			case setPredicateMatched:
				members = append(members, index)
				used = appendUniqueIndex(used, index)
			case setPredicateIndeterminate:
				finding.Missing = append(finding.Missing, setCandidateMissing(candidates[index].ID, missing)...)
				finding.affectedCandidateIndexes = appendUniqueIndex(finding.affectedCandidateIndexes, index)
				used = appendUniqueIndex(used, index)
			case setPredicateMismatch:
			}
		}
		finding.ExistingOccupancy = len(members)
		finding.affectedCandidateIndexes = appendUniqueIndexes(finding.affectedCandidateIndexes, members...)

		for _, override := range rule.CapacityOverrides {
			for index := range candidates {
				result, missing := evaluateSetPredicates(targetFacts, candidateFacts[index], override.CandidatePredicates, "candidate")
				switch result {
				case setPredicateMatched:
					finding.Capacity = min(finding.Capacity, override.Capacity)
					used = appendUniqueIndex(used, index)
				case setPredicateIndeterminate:
					finding.Missing = append(finding.Missing, setCandidateMissing(candidates[index].ID, missing)...)
					finding.affectedCandidateIndexes = appendUniqueIndex(finding.affectedCandidateIndexes, index)
					used = appendUniqueIndex(used, index)
				case setPredicateMismatch:
				}
			}
		}
		if !search.EffectiveComplete() {
			finding.Missing = append(finding.Missing, "search_complete")
		}
		finding.Missing = uniqueSorted(finding.Missing)
		finding.addCandidateEvidence(candidates, candidateFacts, used)
		if len(finding.Missing) > 0 {
			finding.setMissing(finding.Missing)
			findings = append(findings, finding)
			continue
		}
		if len(members) >= finding.Capacity {
			finding.Status = RuleFindingMatched
			finding.Relation = api.DupeRelationManualReview
			finding.ReasonCode = "set_capacity_full"
			findings = append(findings, finding)
			continue
		}
		if finding.MinimumSizeSeparationPercent > 0 && len(members) > 0 {
			separation, ok := minimumSetSizeSeparation(targetFacts, candidateFacts, members)
			if !ok {
				finding.setMissing([]string{"size"})
				findings = append(findings, finding)
				continue
			}
			finding.SeparationKnown = true
			finding.ObservedMinimumSeparationPercent = separation
			if separation < finding.MinimumSizeSeparationPercent {
				finding.Status = RuleFindingMatched
				finding.Relation = api.DupeRelationManualReview
				finding.ReasonCode = "set_size_separation_not_met"
				findings = append(findings, finding)
				continue
			}
		}
		finding.Status = RuleFindingMatched
		finding.Relation = api.DupeRelationCoexists
		finding.ReasonCode = "set_capacity_available"
		findings = append(findings, finding)
	}
	return findings
}

func (finding *SetFinding) setMissing(missing []string) {
	finding.Status = RuleFindingIndeterminate
	finding.Relation = api.DupeRelationInsufficientEvidence
	finding.ReasonCode = "set_evidence_incomplete"
	finding.Missing = uniqueSorted(append(finding.Missing, missing...))
}

func evaluateSetPredicates(
	target normalizedFacts,
	subject normalizedFacts,
	predicates []trackerspkg.DupeSetPredicate,
	missingPrefix string,
) (setPredicateResult, []string) {
	missing := make([]string, 0, len(predicates))
	mismatch := false
	for _, predicate := range predicates {
		targetFact := setDimensionFact(target, predicate.Dimension)
		subjectFact := setDimensionFact(subject, predicate.Dimension)
		if predicate.Optional && targetFact.Status == FactMissing && subjectFact.Status == FactMissing {
			continue
		}
		if predicate.MatchTarget {
			if targetFact.Status == FactContradictory || subjectFact.Status == FactContradictory ||
				targetFact.Status == FactMissing || subjectFact.Status == FactMissing ||
				predicate.RequiresComplete && (targetFact.Status != FactComplete || subjectFact.Status != FactComplete) {
				missing = append(missing, missingPrefix+"_"+string(predicate.Dimension))
				continue
			}
			if compareFacts(targetFact, subjectFact) != DimensionEqual {
				mismatch = true
			}
		}
		if subjectFact.Status == FactContradictory || subjectFact.Status == FactMissing ||
			predicate.RequiresComplete && subjectFact.Status != FactComplete {
			missing = append(missing, missingPrefix+"_"+string(predicate.Dimension))
			continue
		}
		if len(predicate.Values) > 0 && !containsFold(predicate.Values, subjectFact.Value) {
			mismatch = true
		}
		if containsFold(predicate.ExcludedValues, subjectFact.Value) {
			mismatch = true
		}
	}
	if mismatch {
		return setPredicateMismatch, nil
	}
	if len(missing) > 0 {
		return setPredicateIndeterminate, uniqueSorted(missing)
	}
	return setPredicateMatched, nil
}

func setDimensionFact(facts normalizedFacts, dimension trackerspkg.DupeDimension) Fact {
	if dimension == trackerspkg.DupeDimensionHDR {
		return hdrFact(facts.HDR)
	}
	return dimensionFact(facts, dimension)
}

func minimumSetSizeSeparation(target normalizedFacts, candidates []normalizedFacts, members []int) (float64, bool) {
	sizes := make([]int64, 0, len(members)+1)
	targetSize, err := strconv.ParseInt(target.Size.Value, 10, 64)
	if err != nil || target.Size.Status != FactComplete || targetSize <= 0 {
		return 0, false
	}
	sizes = append(sizes, targetSize)
	for _, index := range members {
		size, parseErr := strconv.ParseInt(candidates[index].Size.Value, 10, 64)
		if parseErr != nil || candidates[index].Size.Status != FactComplete || size <= 0 {
			return 0, false
		}
		sizes = append(sizes, size)
	}
	minimum := 100.0
	for left := range sizes {
		for right := left + 1; right < len(sizes); right++ {
			larger, smaller := max(sizes[left], sizes[right]), min(sizes[left], sizes[right])
			minimum = min(minimum, float64(larger-smaller)/float64(larger)*100)
		}
	}
	return minimum, true
}

func applySetFindings(evaluation *Evaluation) {
	if evaluation == nil {
		return
	}
	for _, setFinding := range evaluation.SetFindings {
		for _, index := range setFinding.affectedCandidateIndexes {
			candidate := evaluation.Candidates[index]
			candidate.Findings = append(candidate.Findings, RuleFinding{
				RuleID:     setFinding.RuleID,
				EvidenceID: setFinding.EvidenceID,
				Source:     "tracker_set",
				Status:     setFinding.Status,
				Relation:   setFinding.Relation,
				ReasonCode: setFinding.ReasonCode,
				Compared: []string{
					fmt.Sprintf("set_occupancy=%d/%d", setFinding.ExistingOccupancy, setFinding.Capacity),
					fmt.Sprintf("set_minimum_size_separation=%.2f", setFinding.MinimumSizeSeparationPercent),
				},
				Missing:     append([]string(nil), setFinding.Missing...),
				Priority:    findingPrioritySet,
				Specificity: 1,
				Manual:      setFinding.Relation == api.DupeRelationManualReview,
			})
			evaluation.Candidates[index] = resolveCandidateFindings(
				candidate.Candidate,
				candidate.Facts,
				candidate.Findings,
			)
		}
	}
}

func (finding *SetFinding) addCandidateEvidence(candidates []TrackerCandidate, facts []normalizedFacts, indexes []int) {
	for _, index := range indexes {
		id := strings.TrimSpace(candidates[index].ID)
		if id == "" {
			id = "(missing)"
		}
		finding.CandidateIDs = append(finding.CandidateIDs, id)
		finding.FactSummaries = append(finding.FactSummaries, setFactSummary("candidate", id, facts[index]))
	}
	finding.CandidateIDs = uniqueSorted(finding.CandidateIDs)
	finding.FactSummaries = uniqueSorted(finding.FactSummaries)
}

func setFactSummary(kind string, id string, facts normalizedFacts) string {
	size := facts.Size.Value
	if facts.Size.Status != FactComplete {
		size = "unknown"
	}
	return fmt.Sprintf(
		"%s[id=%s,size=%s,kind=%s,resolution=%s,codec=%s,hdr=%s,edition=%s]",
		kind,
		id,
		size,
		facts.MediaKind,
		facts.Resolution.Value,
		facts.Codec.Value,
		hdrKey(facts.HDR),
		facts.Edition.Value,
	)
}

func setCandidateMissing(id string, missing []string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "(missing)"
	}
	result := make([]string, 0, len(missing))
	for _, value := range missing {
		result = append(result, "candidate="+id+":"+value)
	}
	return result
}

func appendUniqueIndexes(values []int, additions ...int) []int {
	for _, addition := range additions {
		values = appendUniqueIndex(values, addition)
	}
	return values
}

func appendUniqueIndex(values []int, addition int) []int {
	if !slices.Contains(values, addition) {
		return append(values, addition)
	}
	return values
}

func uniqueSorted(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}
