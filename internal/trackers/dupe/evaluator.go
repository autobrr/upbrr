// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"path" //nolint:depguard // Candidate file names are torrent-internal slash data, not local filesystem paths.
	"path/filepath"
	"slices"
	"strings"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Evaluation is one deterministic general-plus-tracker policy result.
type Evaluation struct {
	Candidates     []CandidateEvaluation
	SetFindings    []SetFinding
	RequiresAction bool
	Blocks         bool
	Complete       bool
	TargetFacts    normalizedFacts
}

// CandidateEvaluation binds one candidate to its directional relation and
// retains all private rule findings for diagnostics.
type CandidateEvaluation struct {
	Candidate          TrackerCandidate
	Facts              normalizedFacts
	Relation           api.DupeRelation
	Reasons            []api.DupeReason
	Findings           []RuleFinding
	WinningRule        string
	MatchedRules       []string
	IndeterminateRules []string
}

// Evaluate compares every candidate independently and aggregates
// conservatively. It performs no I/O and consumes no raw protocol payloads.
func Evaluate(
	target api.TrackerDuplicateTarget,
	candidates []TrackerCandidate,
	policy trackerspkg.DupePolicy,
	search SearchEvidence,
) Evaluation {
	targetFacts := normalizeTargetFacts(target)
	evaluation := Evaluation{Complete: search.Complete, TargetFacts: targetFacts}
	candidateFacts := make([]normalizedFacts, 0, len(candidates))
	for _, candidate := range candidates {
		facts := normalizeCandidateFacts(candidate)
		candidateFacts = append(candidateFacts, facts)
		findings := collectCandidateFindings(target, targetFacts, candidate, facts, policy)
		evaluation.Candidates = append(evaluation.Candidates, resolveCandidateFindings(candidate, facts, findings))
	}
	evaluation.SetFindings = evaluateSetRules(targetFacts, candidates, candidateFacts, policy, search)
	applySetFindings(&evaluation)
	for _, candidate := range evaluation.Candidates {
		switch candidate.Relation {
		case api.DupeRelationExactDuplicate, api.DupeRelationExistingPreferred:
			evaluation.Blocks = true
		case api.DupeRelationSameSlot, api.DupeRelationProposedTrumps, api.DupeRelationManualReview,
			api.DupeRelationInsufficientEvidence:
			evaluation.RequiresAction = true
		case api.DupeRelationCoexists:
		}
	}
	for _, finding := range evaluation.SetFindings {
		switch finding.Relation {
		case api.DupeRelationExactDuplicate, api.DupeRelationExistingPreferred:
			evaluation.Blocks = true
		case api.DupeRelationSameSlot, api.DupeRelationProposedTrumps, api.DupeRelationManualReview,
			api.DupeRelationInsufficientEvidence:
			evaluation.RequiresAction = true
		case api.DupeRelationCoexists:
		}
	}
	if !search.Complete {
		evaluation.RequiresAction = true
	}
	return evaluation
}

func exactCandidate(target api.TrackerDuplicateTarget, candidate TrackerCandidate) bool {
	filesComparable := len(target.FileNames) > 0 && len(candidate.Files) > 0
	if filesComparable {
		if candidate.FileCount > 0 && candidate.FileCount != len(candidate.Files) {
			return false
		}
		if len(target.FileNames) != len(candidate.Files) {
			return false
		}
		left := normalizedFileSet(target.FileNames)
		right := normalizedFileSet(candidate.Files)
		if !slices.Equal(left, right) {
			return false
		}
		return !candidate.SizeKnown || target.SizeBytes == 0 || candidate.SizeBytes == target.SizeBytes
	}
	if slices.ContainsFunc(target.Names, func(name string) bool {
		return sameCandidateName(name, candidate.Name)
	}) {
		return true
	}
	return len(target.FileNames) == 1 && releaseNameMatchesFile(target.FileNames[0], candidate.Name)
}

func sameCandidateName(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if strings.EqualFold(left, right) {
		return true
	}
	left = strings.ReplaceAll(left, `\`, "/")
	right = strings.ReplaceAll(right, `\`, "/")
	return strings.EqualFold(path.Base(left), path.Base(right))
}

func releaseNameMatchesFile(fileName string, releaseName string) bool {
	fileBase := filepath.Base(strings.TrimSpace(fileName))
	releaseBase := path.Base(strings.ReplaceAll(strings.TrimSpace(releaseName), `\`, "/"))
	extension := filepath.Ext(fileBase)
	return extension != "" && releaseBase != "" && strings.EqualFold(strings.TrimSuffix(fileBase, extension), releaseBase)
}

func normalizedFileSet(files []string) []string {
	result := make([]string, 0, len(files))
	for _, file := range files {
		if file = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(file, `\`, "/"))); file != "" {
			//pathpolicy:allow Candidate file names are torrent-internal slash data, not local filesystem paths.
			result = append(result, path.Base(file))
		}
	}
	slices.Sort(result)
	return result
}

func sizeVarianceApplies(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	policy trackerspkg.DupePolicy,
) bool {
	if policy.SizeVariancePercent <= 0 {
		return false
	}
	if len(policy.SizeVarianceResolutions) > 0 &&
		(!containsFold(policy.SizeVarianceResolutions, target.Resolution) ||
			!containsFold(policy.SizeVarianceResolutions, candidate.Resolution)) {
		return false
	}
	if len(policy.SizeVarianceTypes) > 0 &&
		(!containsFold(policy.SizeVarianceTypes, target.Type) || !containsFold(policy.SizeVarianceTypes, candidate.Type)) {
		return false
	}
	return true
}

func genericHDRSlot(formats []api.HDRFormat) (string, bool) {
	hasDV := slices.Contains(formats, api.HDRFormatDolbyVision)
	hasGenericHDR := slices.ContainsFunc(formats, func(format api.HDRFormat) bool {
		switch format {
		case api.HDRFormatHDR10, api.HDRFormatHDR10Plus, api.HDRFormatHLG, api.HDRFormatPQ10, api.HDRFormatHDRVivid:
			return true
		case api.HDRFormatSDR, api.HDRFormatDolbyVision, api.HDRFormatWCG:
			return false
		}
		return false
	})
	switch {
	case hasDV && hasGenericHDR:
		return "dv_hdr", true
	case hasDV:
		return "dv", true
	case hasGenericHDR:
		return "hdr", true
	case slices.Contains(formats, api.HDRFormatSDR):
		return "sdr", true
	default:
		return "", false
	}
}

func hdrCompatibility(facts api.HDRFacts) []api.HDRFormat {
	result := append([]api.HDRFormat(nil), facts.Formats...)
	for _, format := range facts.FallbackFormats {
		if !slices.Contains(result, format) {
			result = append(result, format)
		}
	}
	if slices.Contains(result, api.HDRFormatHDR10Plus) && !slices.Contains(result, api.HDRFormatHDR10) {
		result = append(result, api.HDRFormatHDR10)
	}
	return result
}

func strictSuperset(left []api.HDRFormat, right []api.HDRFormat) bool {
	if len(left) <= len(right) {
		return false
	}
	return !slices.ContainsFunc(right, func(value api.HDRFormat) bool {
		return !slices.Contains(left, value)
	})
}

func sameCandidateHDR(left []api.HDRFormat, right []api.HDRFormat) bool {
	return len(left) == len(right) && !slices.ContainsFunc(left, func(format api.HDRFormat) bool {
		return !slices.Contains(right, format)
	})
}

func hdrOverlaps(left []api.HDRFormat, right []api.HDRFormat) bool {
	return slices.ContainsFunc(left, func(format api.HDRFormat) bool {
		return slices.Contains(right, format) || format == api.HDRFormatHDR10 && slices.Contains(right, api.HDRFormatHDR10Plus) ||
			format == api.HDRFormatHDR10Plus && slices.Contains(right, api.HDRFormatHDR10)
	})
}

func hdrKey(facts api.HDRFacts) string {
	values := make([]string, len(facts.Formats))
	for index, format := range facts.Formats {
		values[index] = string(format)
	}
	slices.Sort(values)
	return strings.Join(values, "+")
}

func containsFold(values []string, want string) bool {
	return slices.ContainsFunc(values, func(value string) bool {
		return strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want))
	})
}

func candidateResult(
	candidate TrackerCandidate,
	facts normalizedFacts,
	findings []RuleFinding,
	relation api.DupeRelation,
	reason string,
) CandidateEvaluation {
	result := CandidateEvaluation{
		Candidate:   candidate,
		Facts:       facts,
		Relation:    relation,
		Reasons:     []api.DupeReason{{Code: strings.TrimSpace(reason), Message: dupeReasonMessage(reason)}},
		Findings:    append([]RuleFinding(nil), findings...),
		WinningRule: winningRuleID(findings, relation, reason),
	}
	for _, finding := range findings {
		switch finding.Status {
		case RuleFindingMatched:
			result.MatchedRules = append(result.MatchedRules, finding.RuleID)
		case RuleFindingIndeterminate:
			result.IndeterminateRules = append(result.IndeterminateRules, finding.RuleID)
		case RuleFindingDisproved, RuleFindingNotApplicable:
		}
	}
	slices.Sort(result.MatchedRules)
	slices.Sort(result.IndeterminateRules)
	return result
}

func winningRuleID(findings []RuleFinding, relation api.DupeRelation, reason string) string {
	for _, finding := range findings {
		if findingReason(finding) == reason && findingRelation(finding) == relation {
			return finding.RuleID
		}
	}
	if reason == "policy_conflict" {
		return GeneralPolicyID + "/policy_conflict"
	}
	return ""
}

func dupeReasonMessage(reason string) string {
	switch strings.TrimSpace(reason) {
	case "exact_identity":
		return "Candidate has identical release or file identity."
	case "existing_full_disc":
		return "Tracker permits only one full disc for this work."
	case "existing_season_pack":
		return "Existing season pack contains the proposed episode slot."
	case "proposed_season_pack":
		return "Proposed season pack may supersede the existing episode slot."
	case "target_hdr_contradictory":
		return "Proposed HDR metadata contradicts fallback naming evidence."
	case "hdr_evidence_missing", "target_hdr_missing", "candidate_hdr_missing":
		return "Required HDR evidence is unavailable."
	case "candidate_hdr_contradictory":
		return "Candidate HDR evidence is contradictory."
	case "target_hdr_partial", "candidate_hdr_partial":
		return "Required HDR evidence is only partial."
	case "target_dv_profile_missing", "candidate_dv_profile_missing":
		return "Required Dolby Vision profile evidence is unavailable."
	case "dolby_vision_profile_differs":
		return "Dolby Vision profiles differ and require tracker-policy review."
	case "generic_hdr_ambiguous":
		return "Generic HDR evidence cannot distinguish exact HDR formats."
	case "distinct_hdr_slot":
		return "HDR formats occupy distinct tracker slots."
	case "broader_hdr_compatibility":
		return "Proposed HDR compatibility is broader than the candidate."
	case "existing_broader_hdr_compatibility":
		return "Existing candidate has broader HDR compatibility."
	case "candidate_content_scope_missing":
		return "Candidate content-scope evidence is unavailable."
	case "different_daily_episode":
		return "Candidate is a different daily episode."
	case "different_season":
		return "Candidate is from a different season."
	case "different_episode", "content_scope_differs":
		return "Candidate content coordinates do not overlap the proposed release."
	case "resolution_differs":
		return "Candidate has a different resolution."
	case "media_class_differs":
		return "Candidate occupies a different general media class."
	case "edition_differs":
		return "Candidate is a different edition or cut."
	case "region_differs":
		return "Candidate is from a different source region."
	case "3d_differs":
		return "Candidate differs in 2D/3D presentation."
	case "size_variance":
		return "Absolute size variance satisfies tracker coexistence policy."
	case "candidate_trumpable":
		return "Tracker marks the candidate as potentially trumpable."
	case "set_capacity_available":
		return "Capacity and minimum size separation permit another slot."
	case "set_capacity_full":
		return "The collection-level slot capacity is full and requires review."
	case "set_size_separation_not_met":
		return "The collection does not meet the minimum size separation for another slot."
	case "set_evidence_incomplete":
		return "Collection-level slot evidence is incomplete."
	case "same_tracker_slot":
		return "Candidate occupies the same tracker slot and requires review."
	case "tracker_policy_not_evidence_backed":
		return "Tracker policy evidence is incomplete; manual review is required."
	case "policy_conflict":
		return "Equal-specificity tracker rules conflict; manual review is required."
	case "proposed_webdl_over_webrip":
		return "Proposed WEB-DL is preferred over the existing WEBRip."
	case "existing_webdl_over_webrip":
		return "Existing WEB-DL is preferred over the proposed WEBRip."
	default:
		switch {
		case strings.HasSuffix(reason, "_missing"):
			return "Required comparison evidence is unavailable."
		case strings.HasSuffix(reason, "_contradictory"):
			return "Comparison evidence is contradictory."
		case strings.HasPrefix(reason, "different_"):
			return "Candidate occupies a distinct tracker slot."
		default:
			return "Tracker policy requires review of this candidate."
		}
	}
}

func publicCandidateEvaluations(evaluation Evaluation) []api.DupeCandidateEvaluation {
	result := make([]api.DupeCandidateEvaluation, 0, len(evaluation.Candidates))
	for _, item := range evaluation.Candidates {
		candidate := item.Candidate
		result = append(result, api.DupeCandidateEvaluation{
			ID:             candidate.ID,
			Name:           candidate.Name,
			Link:           sanitizePublicURL(candidate.DetailsLink),
			SizeBytes:      candidate.SizeBytes,
			Relation:       item.Relation,
			Reasons:        append([]api.DupeReason(nil), item.Reasons...),
			Flags:          append([]string(nil), candidate.Flags...),
			HDR:            cloneHDRFacts(item.Facts.HDR),
			Category:       candidate.Category,
			Type:           candidate.Type,
			Resolution:     firstNonEmpty(candidate.Resolution, item.Facts.Resolution.Value),
			Source:         candidate.Source,
			Codec:          candidate.Codec,
			Container:      candidate.Container,
			Provider:       candidate.Provider,
			Group:          candidate.Group,
			Edition:        candidate.Edition,
			Region:         candidate.Region,
			ThreeD:         candidate.ThreeD,
			Repack:         candidate.Repack,
			Season:         candidate.Season,
			Episode:        candidate.Episode,
			Date:           candidate.Date,
			Pack:           candidate.Pack,
			Internal:       candidate.Internal,
			Trumpable:      candidate.Trumpable,
			EvidenceStatus: item.Facts.HDR.Status,
		})
	}
	return result
}
