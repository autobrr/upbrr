// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// RuleFindingStatus is the four-state outcome of one comparison rule.
type RuleFindingStatus string

const (
	// RuleFindingMatched indicates that the rule's relation was established.
	RuleFindingMatched RuleFindingStatus = "matched"
	// RuleFindingDisproved indicates that the rule was disproved by evidence.
	RuleFindingDisproved RuleFindingStatus = "disproved"
	// RuleFindingIndeterminate indicates insufficient evidence to decide.
	RuleFindingIndeterminate RuleFindingStatus = "indeterminate"
	// RuleFindingNotApplicable indicates that the rule does not apply.
	RuleFindingNotApplicable RuleFindingStatus = "not_applicable"
)

// RuleFinding retains one general or tracker-overlay comparison result.
type RuleFinding struct {
	RuleID           string
	EvidenceID       string
	Source           string
	Status           RuleFindingStatus
	Relation         api.DupeRelation
	ReasonCode       string
	Compared         []string
	Missing          []string
	Contradictions   []string
	Priority         int
	Specificity      int
	Manual           bool
	OverridesGeneral bool
	comparisons      []factComparison
}

type factComparison struct {
	Dimension trackerspkg.DupeDimension
	Target    Fact
	Candidate Fact
	Result    DimensionComparison
}

const (
	findingPriorityExact           = 1000
	findingPriorityDisjointContent = 900
	findingPriorityPackContainment = 850
	findingPriorityTrackerRule     = 810
	findingPriorityTrackerMatched  = 800
	findingPriorityTrackerMissing  = 700
	findingPriorityGeneral         = 600
	findingPriorityContradiction   = 550
	findingPrioritySlotMissing     = 500
	findingPriorityTrumpable       = 350
	findingPrioritySize            = 300
	findingPriorityFallback        = 100
)

func collectCandidateFindings(
	target api.TrackerDuplicateTarget,
	targetFacts normalizedFacts,
	candidate TrackerCandidate,
	candidateFacts normalizedFacts,
	policy trackerspkg.DupePolicy,
	workScope WorkScope,
) []RuleFinding {
	findings := make([]RuleFinding, 0, 12)
	findings = append(findings, collectExactFindings(target, candidate)...)
	findings = append(findings, collectGeneralFindings(targetFacts, candidateFacts, policy, workScope)...)
	findings = append(findings, collectTrackerRules(target, targetFacts, candidate, candidateFacts, policy)...)
	if finding, ok := collectTrackerSlotFinding(target, targetFacts, candidate, candidateFacts, policy); ok {
		findings = append(findings, finding)
	}
	if finding, ok := collectSizeFinding(target, candidate, policy); ok {
		findings = append(findings, finding)
	}
	if candidate.Trumpable {
		priority := findingPriorityTrumpable
		if policy.TrumpableOverridesSlot {
			priority = findingPriorityTrackerMatched + 10
		}
		findings = append(findings, RuleFinding{
			RuleID:     "tracker/candidate_trumpable",
			EvidenceID: policy.EvidenceID,
			Source:     "tracker",
			Status:     RuleFindingMatched,
			Relation:   api.DupeRelationProposedTrumps,
			ReasonCode: "candidate_trumpable",
			Priority:   priority,
		})
	}
	findings = append(findings, sameSlotFallbackFinding())
	return findings
}

func collectExactFindings(target api.TrackerDuplicateTarget, candidate TrackerCandidate) []RuleFinding {
	if !exactCandidate(target, candidate) {
		return nil
	}
	return []RuleFinding{{
		RuleID:     GeneralPolicyID + "/exact_identity",
		Source:     "general",
		Status:     RuleFindingMatched,
		Relation:   api.DupeRelationExactDuplicate,
		ReasonCode: "exact_identity",
		Priority:   findingPriorityExact,
	}}
}

func collectGeneralFindings(target normalizedFacts, candidate normalizedFacts, policy trackerspkg.DupePolicy, workScope WorkScope) []RuleFinding {
	findings := make([]RuleFinding, 0, 8)
	switch compareContentScopes(target.Content, candidate.Content) {
	case contentDefinitelyDisjoint:
		reason := "content_scope_differs"
		switch {
		case target.Content.Kind == contentScopeDaily && candidate.Content.Kind == contentScopeDaily:
			reason = "different_daily_episode"
		case target.Content.Season > 0 && candidate.Content.Season > 0 && target.Content.Season != candidate.Content.Season:
			reason = "different_season"
		case target.Content.EpisodeStart > 0 && candidate.Content.EpisodeStart > 0:
			reason = "different_episode"
		}
		findings = append(findings, generalFinding("content_scope", reason, findingPriorityDisjointContent))
	case contentUnknown:
		if target.Content.Kind != contentScopeWork {
			missing := "candidate_content_scope_missing"
			findings = append(findings, RuleFinding{
				RuleID:     GeneralPolicyID + "/content_scope",
				Source:     "general",
				Status:     RuleFindingIndeterminate,
				Relation:   api.DupeRelationInsufficientEvidence,
				ReasonCode: missing,
				Missing:    []string{"content_scope"},
				Priority:   findingPrioritySlotMissing,
			})
		}
	case contentEqual, contentOverlaps:
	}
	// Pack containment reasons about work-level content coverage, so it only
	// applies when the search authoritatively bound candidates to the same
	// work; season numbers alone cannot relate releases across works on a
	// title-fallback search.
	if workScope == WorkScopeProviderID || workScope == WorkScopeTrackerGroup {
		if finding, ok := collectPackContainmentFinding(target.Content, candidate.Content); ok {
			findings = append(findings, finding)
		}
	}
	if target.Resolution.Status == FactContradictory || candidate.Resolution.Status == FactContradictory {
		findings = append(findings, contradictionFinding("resolution"))
	} else if !generalDimensionSuppressed(policy, trackerspkg.DupeDimensionResolution) &&
		generalDimensionDiffers(trackerspkg.DupeDimensionResolution, target.Resolution, candidate.Resolution) {
		findings = append(findings, generalFinding("resolution", "resolution_differs", findingPriorityGeneral))
	}
	if !generalDimensionSuppressed(policy, trackerspkg.DupeDimensionMediaClass) && target.MediaClass != mediaClassUnknown &&
		candidate.MediaClass != mediaClassUnknown && target.MediaClass != candidate.MediaClass {
		findings = append(findings, generalFinding("media_class", "media_class_differs", findingPriorityGeneral))
	}
	for _, dimension := range []struct {
		name      string
		policyKey trackerspkg.DupeDimension
		target    Fact
		other     Fact
		reason    string
	}{
		{
			name:      "edition",
			policyKey: trackerspkg.DupeDimensionEdition,
			target:    target.Edition,
			other:     candidate.Edition,
			reason:    "edition_differs",
		},
		{
			name:      "region",
			policyKey: trackerspkg.DupeDimensionRegion,
			target:    target.Region,
			other:     candidate.Region,
			reason:    "region_differs",
		},
		{
			name:      "3d",
			policyKey: trackerspkg.DupeDimensionThreeD,
			target:    target.ThreeD,
			other:     candidate.ThreeD,
			reason:    "3d_differs",
		},
	} {
		switch {
		case dimension.target.Status == FactContradictory || dimension.other.Status == FactContradictory:
			findings = append(findings, contradictionFinding(dimension.name))
		case !generalDimensionSuppressed(policy, dimension.policyKey) &&
			generalDimensionDiffers(dimension.policyKey, dimension.target, dimension.other):
			findings = append(findings, generalFinding(dimension.name, dimension.reason, findingPriorityGeneral))
		}
	}
	if !generalDimensionSuppressed(policy, trackerspkg.DupeDimensionHDR) {
		if finding, ok := collectGeneralHDRFinding(target, candidate); ok {
			findings = append(findings, finding)
		}
	}
	return findings
}

func collectPackContainmentFinding(target contentScope, candidate contentScope) (RuleFinding, bool) {
	if target.Season <= 0 || candidate.Season <= 0 || target.Season != candidate.Season {
		return RuleFinding{}, false
	}
	var relation api.DupeRelation
	var reason string
	switch {
	case target.Kind == contentScopeSeasonPack &&
		(candidate.Kind == contentScopeEpisode || candidate.Kind == contentScopeEpisodeRange):
		relation, reason = api.DupeRelationCoexists, "proposed_season_pack"
	case candidate.Kind == contentScopeSeasonPack &&
		(target.Kind == contentScopeEpisode || target.Kind == contentScopeEpisodeRange):
		relation, reason = api.DupeRelationExistingPreferred, "existing_season_pack"
	default:
		return RuleFinding{}, false
	}
	return RuleFinding{
		RuleID:      GeneralPolicyID + "/season_pack_containment",
		Source:      "general",
		Status:      RuleFindingMatched,
		Relation:    relation,
		ReasonCode:  reason,
		Compared:    []string{"content_scope"},
		Priority:    findingPriorityPackContainment,
		Specificity: 1,
	}, true
}

func generalDimensionDiffers(dimension trackerspkg.DupeDimension, target Fact, candidate Fact) bool {
	eligible := dimension == trackerspkg.DupeDimensionResolution || dimension == trackerspkg.DupeDimensionEdition ||
		dimension == trackerspkg.DupeDimensionRegion || dimension == trackerspkg.DupeDimensionThreeD
	return eligible && compareFacts(target, candidate) == DimensionDifferent
}

func collectGeneralHDRFinding(target normalizedFacts, candidate normalizedFacts) (RuleFinding, bool) {
	if target.Resolution.Status != FactComplete || candidate.Resolution.Status != FactComplete ||
		!strings.EqualFold(target.Resolution.Value, "2160p") || !strings.EqualFold(candidate.Resolution.Value, "2160p") {
		return RuleFinding{}, false
	}
	if target.HDR.Status == api.HDREvidenceContradictory || candidate.HDR.Status == api.HDREvidenceContradictory {
		return contradictionFinding("hdr"), true
	}
	if target.HDR.Status != api.HDREvidenceComplete || candidate.HDR.Status != api.HDREvidenceComplete {
		return RuleFinding{}, false
	}
	targetSlot, targetKnown := genericHDRSlot(hdrCompatibility(target.HDR))
	candidateSlot, candidateKnown := genericHDRSlot(hdrCompatibility(candidate.HDR))
	if !targetKnown || !candidateKnown || targetSlot == candidateSlot {
		return RuleFinding{}, false
	}
	finding := generalFinding("hdr", "distinct_hdr_slot", findingPriorityGeneral)
	switch {
	case targetSlot == "dv_hdr" && candidateSlot == "hdr":
		finding.Relation = api.DupeRelationProposedTrumps
		finding.ReasonCode = "broader_hdr_compatibility"
		finding.Priority = findingPriorityTrumpable
	case targetSlot == "hdr" && candidateSlot == "dv_hdr":
		finding.Relation = api.DupeRelationExistingPreferred
		finding.ReasonCode = "existing_broader_hdr_compatibility"
		finding.Priority = findingPriorityTrumpable
	}
	return finding, true
}

func generalDimensionSuppressed(policy trackerspkg.DupePolicy, dimension trackerspkg.DupeDimension) bool {
	return slices.Contains(policy.SuppressGeneralCoexistence, dimension)
}

func generalFinding(dimension string, reason string, priority int) RuleFinding {
	return RuleFinding{
		RuleID:      GeneralPolicyID + "/" + dimension,
		Source:      "general",
		Status:      RuleFindingMatched,
		Relation:    api.DupeRelationCoexists,
		ReasonCode:  reason,
		Compared:    []string{dimension},
		Priority:    priority,
		Specificity: 1,
	}
}

func contradictionFinding(dimension string) RuleFinding {
	return RuleFinding{
		RuleID:         GeneralPolicyID + "/" + dimension,
		Source:         "general",
		Status:         RuleFindingIndeterminate,
		Relation:       api.DupeRelationManualReview,
		ReasonCode:     "candidate_" + dimension + "_contradictory",
		Contradictions: []string{dimension},
		Priority:       findingPriorityContradiction,
		Specificity:    1,
	}
}

func collectTrackerRules(
	target api.TrackerDuplicateTarget,
	targetFacts normalizedFacts,
	candidate TrackerCandidate,
	candidateFacts normalizedFacts,
	policy trackerspkg.DupePolicy,
) []RuleFinding {
	total := len(policy.CoexistenceRules) + len(policy.PrecedenceRules) + len(policy.ManualReviewRules)
	findings := make([]RuleFinding, 0, total)
	for _, group := range [][]trackerspkg.DupeRule{policy.CoexistenceRules, policy.PrecedenceRules, policy.ManualReviewRules} {
		for _, rule := range group {
			findings = append(findings, evaluateTrackerRule(target, targetFacts, candidate, candidateFacts, policy, rule))
		}
	}
	return findings
}

func evaluateTrackerRule(
	target api.TrackerDuplicateTarget,
	targetFacts normalizedFacts,
	candidate TrackerCandidate,
	candidateFacts normalizedFacts,
	policy trackerspkg.DupePolicy,
	rule trackerspkg.DupeRule,
) RuleFinding {
	finding := RuleFinding{
		RuleID:           strings.TrimSpace(rule.ID),
		EvidenceID:       firstNonEmpty(rule.EvidenceID, policy.EvidenceID),
		Source:           "tracker",
		Status:           RuleFindingMatched,
		Relation:         api.DupeRelation(strings.TrimSpace(rule.Relation)),
		ReasonCode:       firstNonEmpty(rule.ReasonCode, rule.ID),
		Priority:         rule.Priority,
		Specificity:      len(rule.Conditions),
		Manual:           rule.RequiresManualStep,
		OverridesGeneral: rule.OverridesGeneral,
	}
	if finding.Priority == 0 && finding.Relation == api.DupeRelationExactDuplicate {
		finding.Priority = findingPriorityExact
	} else if finding.Priority == 0 {
		finding.Priority = findingPriorityTrackerRule
	}
	if rule.RequiresManualStep {
		finding.Relation = api.DupeRelationManualReview
	}
	disproved := false
	for _, condition := range rule.Conditions {
		targetFact, candidateFact := comparisonFactsForDimension(
			target,
			targetFacts,
			candidate,
			candidateFacts,
			condition.Dimension,
		)
		comparison := compareFacts(targetFact, candidateFact)
		finding.comparisons = append(finding.comparisons, factComparison{
			Dimension: condition.Dimension,
			Target:    targetFact,
			Candidate: candidateFact,
			Result:    comparison,
		})
		if targetFact.Status == FactContradictory || candidateFact.Status == FactContradictory {
			finding.Contradictions = append(finding.Contradictions, string(condition.Dimension))
			continue
		}
		if condition.MissingNotApplicable && (targetFact.Status == FactMissing || candidateFact.Status == FactMissing) {
			disproved = true
			continue
		}
		if condition.RequiresComplete && (targetFact.Status != FactComplete || candidateFact.Status != FactComplete) {
			finding.Missing = append(finding.Missing, incompleteDimensionName(targetFact, candidateFact, condition.Dimension))
			continue
		}
		if conditionNeedsEvidence(condition) &&
			(comparison == DimensionUnknown || comparison == DimensionNotApplicable) {
			finding.Missing = append(finding.Missing, missingDimensionName(targetFact, candidateFact, condition.Dimension))
			continue
		}
		if len(condition.TargetValues) > 0 && !containsFold(condition.TargetValues, targetFact.Value) {
			disproved = true
		}
		if len(condition.CandidateValues) > 0 && !containsFold(condition.CandidateValues, candidateFact.Value) {
			disproved = true
		}
		if condition.ValuesEqual && comparison != DimensionEqual {
			disproved = true
		}
		if condition.ValuesDifferent && comparison != DimensionDifferent {
			disproved = true
		}
		if comparison == DimensionEqual || comparison == DimensionDifferent {
			finding.Compared = append(finding.Compared, string(condition.Dimension)+"="+string(comparison))
		}
	}
	switch {
	case disproved:
		finding.Status = RuleFindingDisproved
	case len(finding.Missing) > 0 || len(finding.Contradictions) > 0:
		finding.Status = RuleFindingIndeterminate
		if finding.OverridesGeneral {
			finding.Priority = max(finding.Priority, findingPriorityTrackerMissing)
		} else {
			finding.Priority = min(finding.Priority, findingPrioritySlotMissing)
		}
	default:
		finding.Status = RuleFindingMatched
	}
	return finding
}

func collectTrackerSlotFinding(
	target api.TrackerDuplicateTarget,
	targetFacts normalizedFacts,
	candidate TrackerCandidate,
	candidateFacts normalizedFacts,
	policy trackerspkg.DupePolicy,
) (RuleFinding, bool) {
	if len(policy.SlotDimensions) == 0 && len(policy.OptionalSlotDimensions) == 0 && len(policy.RequiredDimensions) == 0 {
		return RuleFinding{}, false
	}
	finding := RuleFinding{
		RuleID:      strings.TrimSpace(policy.ID) + "/slot",
		EvidenceID:  policy.EvidenceID,
		Source:      "tracker",
		Status:      RuleFindingNotApplicable,
		Priority:    findingPriorityTrackerMatched,
		Specificity: len(policy.SlotDimensions) + len(policy.OptionalSlotDimensions) + len(policy.RequiredDimensions),
	}
	for _, configured := range trackerComparisonDimensions(policy) {
		dimension := configured.dimension
		if dimension == trackerspkg.DupeDimensionHDR {
			continue
		}
		targetFact, candidateFact := comparisonFactsForDimension(
			target,
			targetFacts,
			candidate,
			candidateFacts,
			dimension,
		)
		comparison := compareFacts(targetFact, candidateFact)
		if configured.optional && comparison == DimensionNotApplicable {
			continue
		}
		finding.comparisons = append(finding.comparisons, factComparison{
			Dimension: dimension,
			Target:    targetFact,
			Candidate: candidateFact,
			Result:    comparison,
		})
		if targetFact.Status == FactContradictory || candidateFact.Status == FactContradictory {
			finding.Contradictions = append(finding.Contradictions, string(dimension))
			continue
		}
		if configured.requiresComplete && (targetFact.Status != FactComplete || candidateFact.Status != FactComplete) {
			finding.Missing = append(finding.Missing, incompleteDimensionName(targetFact, candidateFact, dimension))
			continue
		}
		switch comparison {
		case DimensionDifferent:
			if configured.required {
				finding.Compared = append(finding.Compared, string(dimension)+"=different")
				continue
			}
			finding.Status = RuleFindingMatched
			finding.Relation = api.DupeRelationCoexists
			finding.ReasonCode = "different_" + string(dimension)
			finding.Compared = append(finding.Compared, string(dimension)+"=different")
			finding.Priority = findingPriorityTrackerMatched
			return finding, true
		case DimensionUnknown, DimensionNotApplicable:
			finding.Missing = append(finding.Missing, missingDimensionName(targetFact, candidateFact, dimension))
		case DimensionEqual:
			finding.Compared = append(finding.Compared, string(dimension)+"=equal")
		}
	}
	if slices.Contains(policy.SlotDimensions, trackerspkg.DupeDimensionHDR) {
		hdrComparison := factComparison{
			Dimension: trackerspkg.DupeDimensionHDR,
			Target:    hdrFact(targetFacts.HDR),
			Candidate: hdrFact(candidateFacts.HDR),
		}
		hdrComparison.Result = compareFacts(hdrComparison.Target, hdrComparison.Candidate)
		finding.comparisons = append(finding.comparisons, hdrComparison)
		hdrFinding, ok := compareTrackerHDR(targetFacts.HDR, candidateFacts.HDR, policy)
		if ok && hdrFinding.Status == RuleFindingMatched {
			hdrFinding.RuleID = finding.RuleID + "/hdr"
			hdrFinding.EvidenceID = policy.EvidenceID
			hdrFinding.Source = "tracker"
			hdrFinding.Specificity = finding.Specificity
			hdrFinding.comparisons = append([]factComparison(nil), finding.comparisons...)
			return hdrFinding, true
		}
		if ok && hdrFinding.Status == RuleFindingIndeterminate {
			finding.Missing = append(finding.Missing, hdrFinding.Missing...)
			finding.Contradictions = append(finding.Contradictions, hdrFinding.Contradictions...)
			finding.ReasonCode = hdrFinding.ReasonCode
		}
	}
	if len(finding.Missing) > 0 || len(finding.Contradictions) > 0 {
		finding.Status = RuleFindingIndeterminate
		finding.Relation = api.DupeRelationInsufficientEvidence
		if len(finding.Contradictions) > 0 && policy.SlotContradictionsRequireManualReview {
			finding.Relation = api.DupeRelationManualReview
		}
		finding.Priority = findingPrioritySlotMissing
		if finding.ReasonCode == "" {
			finding.ReasonCode = missingReasonCode(finding.Missing, finding.Contradictions)
		}
		return finding, true
	}
	finding.Status = RuleFindingMatched
	finding.Relation = api.DupeRelationSameSlot
	finding.ReasonCode = "same_tracker_slot"
	finding.Priority = findingPriorityFallback
	return finding, true
}

type trackerComparisonDimension struct {
	dimension        trackerspkg.DupeDimension
	optional         bool
	required         bool
	requiresComplete bool
}

func trackerComparisonDimensions(policy trackerspkg.DupePolicy) []trackerComparisonDimension {
	result := make([]trackerComparisonDimension, 0, len(policy.SlotDimensions)+len(policy.OptionalSlotDimensions)+len(policy.RequiredDimensions))
	seen := make(map[trackerspkg.DupeDimension]struct{})
	for _, group := range []struct {
		dimensions []trackerspkg.DupeDimension
		optional   bool
		required   bool
	}{
		{dimensions: policy.SlotDimensions},
		{dimensions: policy.OptionalSlotDimensions, optional: true},
		{dimensions: policy.RequiredDimensions, required: true},
	} {
		for _, dimension := range group.dimensions {
			if _, ok := seen[dimension]; ok {
				continue
			}
			seen[dimension] = struct{}{}
			result = append(result, trackerComparisonDimension{
				dimension:        dimension,
				optional:         group.optional,
				required:         group.required,
				requiresComplete: slices.Contains(policy.CompleteSlotDimensions, dimension),
			})
		}
	}
	return result
}

func compareTrackerHDR(target api.HDRFacts, candidate api.HDRFacts, policy trackerspkg.DupePolicy) (RuleFinding, bool) {
	finding := RuleFinding{
		Status:     RuleFindingMatched,
		Relation:   api.DupeRelationCoexists,
		ReasonCode: "distinct_hdr_slot",
		Priority:   findingPriorityTrackerMatched,
	}
	if target.Status == api.HDREvidenceContradictory || candidate.Status == api.HDREvidenceContradictory {
		finding.Status = RuleFindingIndeterminate
		finding.Relation = api.DupeRelationInsufficientEvidence
		finding.ReasonCode = "candidate_hdr_contradictory"
		finding.Contradictions = []string{"hdr"}
		return finding, true
	}
	if target.Status == api.HDREvidenceMissing || candidate.Status == api.HDREvidenceMissing {
		finding.Status = RuleFindingIndeterminate
		finding.Relation = api.DupeRelationInsufficientEvidence
		finding.ReasonCode = hdrMissingReason(target, candidate)
		finding.Missing = []string{strings.TrimSuffix(finding.ReasonCode, "_missing")}
		return finding, true
	}
	if target.Status == api.HDREvidencePartial || candidate.Status == api.HDREvidencePartial {
		switch policy.HDRPartialMode {
		case trackerspkg.DupeHDRPartialGenericMarker:
			if hdrOverlaps(target.Formats, candidate.Formats) {
				finding.Status = RuleFindingIndeterminate
				finding.Relation = api.DupeRelationInsufficientEvidence
				finding.ReasonCode = "generic_hdr_ambiguous"
				finding.Missing = []string{"hdr_exact_format"}
				return finding, true
			}
		case trackerspkg.DupeHDRPartialExplicitTitle:
		case trackerspkg.DupeHDRPartialReject:
			finding.Status = RuleFindingIndeterminate
			finding.Relation = api.DupeRelationInsufficientEvidence
			finding.ReasonCode = "candidate_hdr_partial"
			if target.Status == api.HDREvidencePartial {
				finding.ReasonCode = "target_hdr_partial"
			}
			finding.Missing = []string{"hdr_complete"}
			return finding, true
		}
	}
	if policy.HDRSlotMode == trackerspkg.DupeHDRSlotModeGeneric {
		targetSlot, targetKnown := genericHDRSlot(target.Formats)
		candidateSlot, candidateKnown := genericHDRSlot(candidate.Formats)
		if !targetKnown || !candidateKnown {
			finding.Status = RuleFindingIndeterminate
			finding.Relation = api.DupeRelationInsufficientEvidence
			finding.ReasonCode = "hdr_evidence_missing"
			finding.Missing = []string{"hdr"}
			return finding, true
		}
		if targetSlot == candidateSlot {
			return RuleFinding{}, false
		}
		return finding, true
	}
	if sameCandidateHDR(target.Formats, candidate.Formats) {
		if policy.RequireDolbyVisionProfile && slices.Contains(target.Formats, api.HDRFormatDolbyVision) {
			switch {
			case target.DolbyVisionProfile != "" && candidate.DolbyVisionProfile == "":
				finding.Status = RuleFindingIndeterminate
				finding.Relation = api.DupeRelationInsufficientEvidence
				finding.ReasonCode = "candidate_dv_profile_missing"
				finding.Missing = []string{"candidate_dv_profile"}
				return finding, true
			case target.DolbyVisionProfile == "" && candidate.DolbyVisionProfile != "":
				finding.Status = RuleFindingIndeterminate
				finding.Relation = api.DupeRelationInsufficientEvidence
				finding.ReasonCode = "target_dv_profile_missing"
				finding.Missing = []string{"target_dv_profile"}
				return finding, true
			case policy.DolbyVisionProfile5Slot &&
				(target.DolbyVisionProfile == "5" || candidate.DolbyVisionProfile == "5") &&
				(target.Status != api.HDREvidenceComplete || candidate.Status != api.HDREvidenceComplete):
				finding.Status = RuleFindingIndeterminate
				finding.Relation = api.DupeRelationInsufficientEvidence
				finding.ReasonCode = "dolby_vision_profile_not_authoritative"
				finding.Missing = []string{"dv_profile_complete"}
				return finding, true
			case target.DolbyVisionProfile != candidate.DolbyVisionProfile:
				if policy.DolbyVisionProfile5Slot &&
					(target.DolbyVisionProfile == "5" || candidate.DolbyVisionProfile == "5") {
					finding.Relation = api.DupeRelationCoexists
					finding.ReasonCode = "distinct_dolby_vision_profile_slot"
				} else {
					finding.Relation = api.DupeRelationManualReview
					finding.ReasonCode = "dolby_vision_profile_differs"
				}
				return finding, true
			}
		}
		return RuleFinding{}, false
	}
	if policy.HDRCompatibilityMode == trackerspkg.DupeHDRCompatibilityDirectional {
		targetCompatibility := hdrCompatibility(target)
		candidateCompatibility := hdrCompatibility(candidate)
		switch {
		case strictSuperset(targetCompatibility, candidateCompatibility):
			finding.Relation = api.DupeRelationProposedTrumps
			finding.ReasonCode = "broader_hdr_compatibility"
		case strictSuperset(candidateCompatibility, targetCompatibility):
			finding.Relation = api.DupeRelationExistingPreferred
			finding.ReasonCode = "existing_broader_hdr_compatibility"
		}
	}
	return finding, true
}

func collectSizeFinding(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	policy trackerspkg.DupePolicy,
) (RuleFinding, bool) {
	if !sizeVarianceApplies(target, candidate, policy) || target.SizeBytes <= 0 || !candidate.SizeKnown || candidate.SizeBytes <= 0 {
		return RuleFinding{}, false
	}
	ratio := math.Abs(float64(target.SizeBytes-candidate.SizeBytes)) / float64(max(target.SizeBytes, candidate.SizeBytes)) * 100
	if ratio < policy.SizeVariancePercent {
		return RuleFinding{}, false
	}
	return RuleFinding{
		RuleID:      strings.TrimSpace(policy.ID) + "/size_variance",
		EvidenceID:  policy.EvidenceID,
		Source:      "tracker",
		Status:      RuleFindingMatched,
		Relation:    api.DupeRelationCoexists,
		ReasonCode:  "size_variance",
		Compared:    []string{fmt.Sprintf("size_variance=%.2f", ratio)},
		Priority:    findingPrioritySize,
		Specificity: 1,
	}, true
}

func sameSlotFallbackFinding() RuleFinding {
	return RuleFinding{
		RuleID:     GeneralPolicyID + "/same_slot",
		Source:     "general",
		Status:     RuleFindingMatched,
		Relation:   api.DupeRelationSameSlot,
		ReasonCode: "same_tracker_slot",
		Priority:   findingPriorityFallback,
	}
}

func resolveCandidateFindings(candidate TrackerCandidate, facts normalizedFacts, findings []RuleFinding) CandidateEvaluation {
	applicable := make([]RuleFinding, 0, len(findings))
	for _, finding := range findings {
		if finding.Status == RuleFindingMatched || finding.Status == RuleFindingIndeterminate {
			applicable = append(applicable, finding)
		}
	}
	sort.SliceStable(applicable, func(left int, right int) bool {
		if applicable[left].Priority != applicable[right].Priority {
			return applicable[left].Priority > applicable[right].Priority
		}
		if applicable[left].Specificity != applicable[right].Specificity {
			return applicable[left].Specificity > applicable[right].Specificity
		}
		return applicable[left].RuleID < applicable[right].RuleID
	})
	if len(applicable) == 0 {
		return candidateResult(candidate, facts, findings, api.DupeRelationSameSlot, "same_tracker_slot")
	}
	top := applicable[0]
	topRelation := findingRelation(top)
	for _, finding := range applicable[1:] {
		if finding.Priority != top.Priority || finding.Specificity != top.Specificity {
			break
		}
		if relation := findingRelation(finding); relation != topRelation {
			return candidateResult(candidate, facts, findings, api.DupeRelationManualReview, "policy_conflict")
		}
	}
	return candidateResult(candidate, facts, findings, topRelation, findingReason(top))
}

func findingRelation(finding RuleFinding) api.DupeRelation {
	if finding.Status == RuleFindingIndeterminate {
		if finding.Relation == api.DupeRelationManualReview {
			return api.DupeRelationManualReview
		}
		return api.DupeRelationInsufficientEvidence
	}
	return finding.Relation
}

func findingReason(finding RuleFinding) string {
	if finding.Status != RuleFindingIndeterminate {
		return finding.ReasonCode
	}
	if strings.TrimSpace(finding.ReasonCode) != "" {
		return finding.ReasonCode
	}
	return missingReasonCode(finding.Missing, finding.Contradictions)
}

func comparisonFactsForDimension(
	target api.TrackerDuplicateTarget,
	targetFacts normalizedFacts,
	candidate TrackerCandidate,
	candidateFacts normalizedFacts,
	dimension trackerspkg.DupeDimension,
) (Fact, Fact) {
	switch dimension {
	case trackerspkg.DupeDimensionPack:
		return boolFact(target.Pack), boolFact(candidate.Pack)
	case trackerspkg.DupeDimensionSeason:
		return contentSeasonFact(targetFacts.Content), contentSeasonFact(candidateFacts.Content)
	case trackerspkg.DupeDimensionEpisode:
		return contentEpisodeFact(targetFacts.Content), contentEpisodeFact(candidateFacts.Content)
	case trackerspkg.DupeDimensionDate:
		return completeFact(target.Date, FactOriginTargetMedia, "date"), completeFact(candidate.Date, FactOriginTrackerAPI, "date")
	case trackerspkg.DupeDimensionHDR:
		return hdrFact(targetFacts.HDR), hdrFact(candidateFacts.HDR)
	case trackerspkg.DupeDimensionType,
		trackerspkg.DupeDimensionSource,
		trackerspkg.DupeDimensionMediaKind,
		trackerspkg.DupeDimensionMediaClass,
		trackerspkg.DupeDimensionSourceFamily,
		trackerspkg.DupeDimensionResolution,
		trackerspkg.DupeDimensionCodec,
		trackerspkg.DupeDimensionContainer,
		trackerspkg.DupeDimensionEdition,
		trackerspkg.DupeDimensionRegion,
		trackerspkg.DupeDimensionThreeD,
		trackerspkg.DupeDimensionProvider,
		trackerspkg.DupeDimensionGroup,
		trackerspkg.DupeDimensionRepack,
		trackerspkg.DupeDimensionSize:
		return dimensionFact(targetFacts, dimension), dimensionFact(candidateFacts, dimension)
	}
	return missingFact(), missingFact()
}

func hdrFact(facts api.HDRFacts) Fact {
	status := FactStatus(facts.Status)
	if status == "" {
		status = FactMissing
	}
	return Fact{
		Value:          hdrKey(facts),
		Status:         status,
		Origin:         FactOriginUnknown,
		SourceFields:   append([]string(nil), facts.SourceFields...),
		Contradictions: append([]string(nil), facts.Contradictions...),
	}
}

func boolFact(value bool) Fact {
	return Fact{
		Value:  strconv.FormatBool(value),
		Status: FactComplete,
		Origin: FactOriginTrackerAPI,
	}
}

func contentSeasonFact(scope contentScope) Fact {
	if !contentSeasonKnown(scope) {
		return missingFact()
	}
	return Fact{
		Value:  strconv.Itoa(scope.Season),
		Status: FactComplete,
		Origin: scope.Origin,
	}
}

func contentEpisodeFact(scope contentScope) Fact {
	if scope.EpisodeStart <= 0 {
		return missingFact()
	}
	return Fact{
		Value:  strconv.Itoa(scope.EpisodeStart),
		Status: FactComplete,
		Origin: scope.Origin,
	}
}

func conditionNeedsEvidence(condition trackerspkg.DupeCondition) bool {
	return condition.RequiresComplete || len(condition.TargetValues) > 0 || len(condition.CandidateValues) > 0 ||
		condition.ValuesEqual || condition.ValuesDifferent
}

func missingDimensionName(target Fact, candidate Fact, dimension trackerspkg.DupeDimension) string {
	switch {
	case target.Status == FactMissing:
		return "target_" + string(dimension)
	case candidate.Status == FactMissing:
		return "candidate_" + string(dimension)
	case target.Status == FactContradictory:
		return "target_" + string(dimension) + "_contradictory"
	case candidate.Status == FactContradictory:
		return "candidate_" + string(dimension) + "_contradictory"
	default:
		return string(dimension)
	}
}

func incompleteDimensionName(target Fact, candidate Fact, dimension trackerspkg.DupeDimension) string {
	switch {
	case target.Status == FactPartial:
		return "target_" + string(dimension) + "_partial"
	case candidate.Status == FactPartial:
		return "candidate_" + string(dimension) + "_partial"
	default:
		return missingDimensionName(target, candidate, dimension)
	}
}

func missingReasonCode(missing []string, contradictions []string) string {
	if len(contradictions) > 0 {
		return "candidate_" + strings.TrimPrefix(contradictions[0], "candidate_") + "_contradictory"
	}
	if len(missing) == 0 {
		return "insufficient_evidence"
	}
	value := strings.TrimSpace(missing[0])
	if strings.HasSuffix(value, "_missing") || strings.HasSuffix(value, "_partial") {
		return value
	}
	return value + "_missing"
}

func hdrMissingReason(target api.HDRFacts, candidate api.HDRFacts) string {
	if target.Status == api.HDREvidenceMissing {
		return "target_hdr_missing"
	}
	if candidate.Status == api.HDREvidenceMissing {
		return "candidate_hdr_missing"
	}
	return "hdr_evidence_missing"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
