// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"math"
	"path" //nolint:depguard // Candidate file names are torrent-internal slash data, not local filesystem paths.
	"slices"
	"strconv"
	"strings"

	trackerspkg "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Evaluation is one deterministic tracker-policy result.
type Evaluation struct {
	Candidates     []CandidateEvaluation
	RequiresAction bool
	Blocks         bool
	Complete       bool
}

// CandidateEvaluation binds one candidate to its directional relation.
type CandidateEvaluation struct {
	Candidate TrackerCandidate
	Relation  api.DupeRelation
	Reasons   []api.DupeReason
}

// Evaluate compares every candidate independently and aggregates
// conservatively. It performs no I/O and consumes no raw protocol payloads.
func Evaluate(
	target api.TrackerDuplicateTarget,
	candidates []TrackerCandidate,
	policy trackerspkg.DupePolicy,
	search SearchEvidence,
) Evaluation {
	evaluation := Evaluation{Complete: search.Complete}
	for _, candidate := range candidates {
		evaluation.Candidates = append(evaluation.Candidates, evaluateCandidate(target, candidate, policy))
	}
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
	if !search.Complete {
		evaluation.RequiresAction = true
	}
	return evaluation
}

func evaluateCandidate(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	policy trackerspkg.DupePolicy,
) CandidateEvaluation {
	if exactCandidate(target, candidate) {
		return candidateResult(candidate, api.DupeRelationExactDuplicate, "exact_identity", "candidate has identical release or file identity")
	}
	if relation, reason, ok := episodeIdentityRelation(target, candidate); ok {
		return candidateResult(candidate, relation, reason, "")
	}
	if target.HDR.Status == api.HDREvidenceContradictory {
		return candidateResult(candidate, api.DupeRelationManualReview, "target_hdr_contradictory", "")
	}
	if relation, reason, ok := evaluateDeclarativeRules(target, candidate, policy.CoexistenceRules); ok {
		return candidateResult(candidate, relation, reason, "")
	}
	if relation, reason, ok := evaluateDeclarativeRules(target, candidate, policy.PrecedenceRules); ok {
		return candidateResult(candidate, relation, reason, "")
	}
	if relation, reason, ok := evaluateDeclarativeRules(target, candidate, policy.ManualReviewRules); ok {
		return candidateResult(candidate, relation, reason, "")
	}
	if reason := missingRequiredEvidence(target, candidate, policy.RequiredEvidence); reason != "" {
		return candidateResult(candidate, api.DupeRelationInsufficientEvidence, reason, "")
	}
	if relation, reason, ok := structuralSlotRelation(target, candidate, policy.SlotDimensions); ok {
		return candidateResult(candidate, relation, reason, "")
	}
	if relation, reason, ok := hdrRelation(target.HDR, candidate.HDR, policy); ok {
		return candidateResult(candidate, relation, reason, "")
	}
	if sizeVarianceApplies(target, candidate, policy) && target.SizeBytes > 0 && candidate.SizeKnown && candidate.SizeBytes > 0 {
		ratio := math.Abs(float64(target.SizeBytes-candidate.SizeBytes)) / float64(max(target.SizeBytes, candidate.SizeBytes)) * 100
		if ratio >= policy.SizeVariancePercent {
			return candidateResult(candidate, api.DupeRelationCoexists, "size_variance", "")
		}
	}
	if candidate.Trumpable {
		return candidateResult(candidate, api.DupeRelationProposedTrumps, "candidate_trumpable", "")
	}
	if candidate.HDR.Status == api.HDREvidenceMissing && target.HDR.Status != api.HDREvidenceMissing {
		return candidateResult(candidate, api.DupeRelationInsufficientEvidence, "candidate_hdr_missing", "")
	}
	return candidateResult(candidate, api.DupeRelationSameSlot, "same_tracker_slot", "")
}

func episodeIdentityRelation(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
) (api.DupeRelation, string, bool) {
	if target.Date != "" {
		switch {
		case candidate.Date == "":
			return api.DupeRelationInsufficientEvidence, "candidate_date_missing", true
		case !strings.EqualFold(strings.TrimSpace(target.Date), strings.TrimSpace(candidate.Date)):
			return api.DupeRelationCoexists, "different_daily_episode", true
		}
	}
	if target.Season <= 0 {
		return "", "", false
	}
	switch {
	case candidate.Season <= 0:
		return api.DupeRelationInsufficientEvidence, "candidate_season_missing", true
	case target.Season != candidate.Season:
		return api.DupeRelationCoexists, "different_season", true
	case target.Pack || candidate.Pack || target.Episode <= 0:
		return "", "", false
	case candidate.Episode <= 0:
		return api.DupeRelationInsufficientEvidence, "candidate_episode_missing", true
	case target.Episode != candidate.Episode:
		return api.DupeRelationCoexists, "different_episode", true
	default:
		return "", "", false
	}
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

func exactCandidate(target api.TrackerDuplicateTarget, candidate TrackerCandidate) bool {
	for _, name := range target.Names {
		if sameCandidateName(name, candidate.Name) {
			return true
		}
	}
	if len(target.FileNames) == 0 || len(candidate.Files) == 0 || len(target.FileNames) != len(candidate.Files) {
		return false
	}
	left := normalizedFileSet(target.FileNames)
	right := normalizedFileSet(candidate.Files)
	return slices.Equal(left, right) && (!candidate.SizeKnown || target.SizeBytes == 0 || candidate.SizeBytes == target.SizeBytes)
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
	return strings.EqualFold(strings.TrimSuffix(path.Base(left), path.Ext(left)), strings.TrimSuffix(path.Base(right), path.Ext(right)))
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

func hdrRelation(target api.HDRFacts, candidate api.HDRFacts, policy trackerspkg.DupePolicy) (api.DupeRelation, string, bool) {
	if target.Status == api.HDREvidenceMissing || candidate.Status == api.HDREvidenceMissing {
		if policy.RequiredEvidence.HDR {
			return api.DupeRelationInsufficientEvidence, "hdr_evidence_missing", true
		}
		return "", "", false
	}
	if candidate.Status == api.HDREvidenceContradictory {
		return api.DupeRelationInsufficientEvidence, "candidate_hdr_contradictory", true
	}
	policyID := strings.ToLower(policy.ID)
	if policy.RequiredEvidence.HDR && target.Status == api.HDREvidencePartial {
		return api.DupeRelationInsufficientEvidence, "target_hdr_partial", true
	}
	if strings.Contains(policyID, "ant/") && (target.Status == api.HDREvidencePartial || candidate.Status == api.HDREvidencePartial) {
		if hdrOverlaps(target.Formats, candidate.Formats) {
			return api.DupeRelationInsufficientEvidence, "ant_generic_hdr_ambiguous", true
		}
		return api.DupeRelationCoexists, "ant_distinct_hdr_slot", true
	}
	if policy.RequiredEvidence.HDR && candidate.Status == api.HDREvidencePartial && !strings.Contains(policyID, "mtv/") {
		return api.DupeRelationInsufficientEvidence, "candidate_hdr_partial", true
	}
	if policy.HDRSlotMode == trackerspkg.DupeHDRSlotModeGeneric {
		targetSlot, targetKnown := genericHDRSlot(target.Formats)
		candidateSlot, candidateKnown := genericHDRSlot(candidate.Formats)
		if !targetKnown || !candidateKnown {
			return api.DupeRelationInsufficientEvidence, "hdr_evidence_missing", true
		}
		if targetSlot == candidateSlot {
			return "", "", false
		}
		return api.DupeRelationCoexists, "distinct_hdr_slot", true
	}
	if strings.Contains(policyID, "lume/") && sameCandidateHDR(target.Formats, candidate.Formats) &&
		slices.Contains(target.Formats, api.HDRFormatDolbyVision) {
		switch {
		case target.DolbyVisionProfile != "" && candidate.DolbyVisionProfile == "":
			return api.DupeRelationInsufficientEvidence, "candidate_dv_profile_missing", true
		case target.DolbyVisionProfile == "" && candidate.DolbyVisionProfile != "":
			return api.DupeRelationInsufficientEvidence, "target_dv_profile_missing", true
		case target.DolbyVisionProfile != candidate.DolbyVisionProfile:
			return api.DupeRelationManualReview, "dolby_vision_profile_differs", true
		}
	}
	if sameCandidateHDR(target.Formats, candidate.Formats) {
		return "", "", false
	}
	if strings.Contains(policyID, "mtv/") || strings.Contains(policyID, "lume/") {
		targetCompatibility := hdrCompatibility(target)
		candidateCompatibility := hdrCompatibility(candidate)
		switch {
		case strictSuperset(targetCompatibility, candidateCompatibility):
			return api.DupeRelationProposedTrumps, "broader_hdr_compatibility", true
		case strictSuperset(candidateCompatibility, targetCompatibility):
			return api.DupeRelationExistingPreferred, "existing_broader_hdr_compatibility", true
		default:
			return api.DupeRelationCoexists, "distinct_hdr_slot", true
		}
	}
	return api.DupeRelationCoexists, "distinct_hdr_slot", true
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
	for _, value := range right {
		if !slices.Contains(left, value) {
			return false
		}
	}
	return true
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

func structuralSlotRelation(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	dimensions []trackerspkg.DupeDimension,
) (api.DupeRelation, string, bool) {
	for _, dimension := range dimensions {
		if dimension == trackerspkg.DupeDimensionHDR {
			continue
		}
		targetValue, candidateValue := dimensionValues(target, candidate, dimension)
		if targetValue == "" || candidateValue == "" {
			continue
		}
		if !strings.EqualFold(targetValue, candidateValue) {
			return api.DupeRelationCoexists, "different_" + string(dimension), true
		}
	}
	return "", "", false
}

func missingRequiredEvidence(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	required trackerspkg.DupeEvidenceRequirements,
) string {
	switch {
	case required.HDR && target.HDR.Status == api.HDREvidenceMissing:
		return "target_hdr_missing"
	case required.HDR && candidate.HDR.Status == api.HDREvidenceMissing:
		return "candidate_hdr_missing"
	case required.Size && target.SizeBytes <= 0:
		return "target_size_missing"
	case required.Size && !candidate.SizeKnown:
		return "candidate_size_missing"
	case required.Files && len(target.FileNames) == 0:
		return "target_files_missing"
	case required.Files && len(candidate.Files) == 0:
		return "candidate_files_missing"
	case required.Type && target.Type == "":
		return "target_type_missing"
	case required.Type && candidate.Type == "":
		return "candidate_type_missing"
	case required.Source && target.Source == "":
		return "target_source_missing"
	case required.Source && candidate.Source == "":
		return "candidate_source_missing"
	case required.Resolution && target.Resolution == "":
		return "target_resolution_missing"
	case required.Resolution && candidate.Resolution == "":
		return "candidate_resolution_missing"
	case required.Codec && target.VideoCodec == "":
		return "target_codec_missing"
	case required.Codec && candidate.Codec == "":
		return "candidate_codec_missing"
	case required.Container && target.Container == "":
		return "target_container_missing"
	case required.Container && candidate.Container == "":
		return "candidate_container_missing"
	case required.Provider && target.Provider == "":
		return "target_provider_missing"
	case required.Provider && candidate.Provider == "":
		return "candidate_provider_missing"
	case required.Group && target.Group == "":
		return "target_group_missing"
	case required.Group && candidate.Group == "":
		return "candidate_group_missing"
	case required.Edition && target.Edition == "":
		return "target_edition_missing"
	case required.Edition && candidate.Edition == "":
		return "candidate_edition_missing"
	case required.Region && target.Region == "":
		return "target_region_missing"
	case required.Region && candidate.Region == "":
		return "candidate_region_missing"
	case required.ThreeD && target.ThreeD == "":
		return "target_3d_missing"
	case required.ThreeD && candidate.ThreeD == "":
		return "candidate_3d_missing"
	case required.Repack && target.Repack == "":
		return "target_repack_missing"
	case required.Repack && candidate.Repack == "":
		return "candidate_repack_missing"
	default:
		return ""
	}
}

func evaluateDeclarativeRules(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	rules []trackerspkg.DupeRule,
) (api.DupeRelation, string, bool) {
	for _, rule := range rules {
		if !matchesDeclarativeRule(target, candidate, rule) {
			continue
		}
		relation := api.DupeRelation(strings.TrimSpace(rule.Relation))
		if rule.RequiresManualStep {
			relation = api.DupeRelationManualReview
		}
		if relation == "" {
			continue
		}
		reason := strings.TrimSpace(rule.ReasonCode)
		if reason == "" {
			reason = strings.TrimSpace(rule.ID)
		}
		return relation, reason, true
	}
	return "", "", false
}

func matchesDeclarativeRule(target api.TrackerDuplicateTarget, candidate TrackerCandidate, rule trackerspkg.DupeRule) bool {
	for _, condition := range rule.Conditions {
		targetValue, candidateValue := dimensionValues(target, candidate, condition.Dimension)
		if condition.RequiresComplete && (targetValue == "" || candidateValue == "") {
			return false
		}
		if len(condition.TargetValues) > 0 && !containsFold(condition.TargetValues, targetValue) {
			return false
		}
		if len(condition.CandidateValues) > 0 && !containsFold(condition.CandidateValues, candidateValue) {
			return false
		}
		if condition.ValuesEqual && !strings.EqualFold(strings.TrimSpace(targetValue), strings.TrimSpace(candidateValue)) {
			return false
		}
		if condition.ValuesDifferent && strings.EqualFold(strings.TrimSpace(targetValue), strings.TrimSpace(candidateValue)) {
			return false
		}
	}
	return true
}

func dimensionValues(
	target api.TrackerDuplicateTarget,
	candidate TrackerCandidate,
	dimension trackerspkg.DupeDimension,
) (string, string) {
	switch dimension {
	case trackerspkg.DupeDimensionType:
		return target.Type, candidate.Type
	case trackerspkg.DupeDimensionSource:
		return target.Source, candidate.Source
	case trackerspkg.DupeDimensionResolution:
		return target.Resolution, candidate.Resolution
	case trackerspkg.DupeDimensionCodec:
		return target.VideoCodec, candidate.Codec
	case trackerspkg.DupeDimensionContainer:
		return target.Container, candidate.Container
	case trackerspkg.DupeDimensionEdition:
		return target.Edition, candidate.Edition
	case trackerspkg.DupeDimensionRegion:
		return target.Region, candidate.Region
	case trackerspkg.DupeDimensionThreeD:
		return target.ThreeD, candidate.ThreeD
	case trackerspkg.DupeDimensionProvider:
		return target.Provider, candidate.Provider
	case trackerspkg.DupeDimensionGroup:
		return target.Group, candidate.Group
	case trackerspkg.DupeDimensionPack:
		return boolString(target.Pack), boolString(candidate.Pack)
	case trackerspkg.DupeDimensionSeason:
		return positiveIntString(target.Season), positiveIntString(candidate.Season)
	case trackerspkg.DupeDimensionEpisode:
		return positiveIntString(target.Episode), positiveIntString(candidate.Episode)
	case trackerspkg.DupeDimensionDate:
		return target.Date, candidate.Date
	case trackerspkg.DupeDimensionHDR:
		return hdrKey(target.HDR), hdrKey(candidate.HDR)
	default:
		return "", ""
	}
}

func hdrKey(facts api.HDRFacts) string {
	values := make([]string, len(facts.Formats))
	for index, format := range facts.Formats {
		values[index] = string(format)
	}
	slices.Sort(values)
	return strings.Join(values, "+")
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func positiveIntString(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func containsFold(values []string, want string) bool {
	return slices.ContainsFunc(values, func(value string) bool {
		return strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want))
	})
}

func candidateResult(candidate TrackerCandidate, relation api.DupeRelation, reason string, message string) CandidateEvaluation {
	if strings.TrimSpace(message) == "" {
		message = dupeReasonMessage(reason)
	}
	return CandidateEvaluation{
		Candidate: candidate,
		Relation:  relation,
		Reasons: []api.DupeReason{{
			Code:    strings.TrimSpace(reason),
			Message: strings.TrimSpace(message),
		}},
	}
}

func dupeReasonMessage(reason string) string {
	switch strings.TrimSpace(reason) {
	case "exact_identity":
		return "Candidate has identical release or file identity."
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
	case "ant_generic_hdr_ambiguous":
		return "ANT generic HDR evidence cannot distinguish HDR10 from HDR10+."
	case "ant_distinct_hdr_slot", "distinct_hdr_slot":
		return "HDR formats occupy distinct tracker slots."
	case "broader_hdr_compatibility":
		return "Proposed HDR compatibility is broader than the candidate."
	case "existing_broader_hdr_compatibility":
		return "Existing candidate has broader HDR compatibility."
	case "candidate_size_missing", "target_size_missing":
		return "Required size evidence is unavailable."
	case "candidate_files_missing", "target_files_missing":
		return "Required file identity evidence is unavailable."
	case "candidate_type_missing", "target_type_missing":
		return "Required release-type evidence is unavailable."
	case "candidate_source_missing", "target_source_missing":
		return "Required source evidence is unavailable."
	case "candidate_resolution_missing", "target_resolution_missing":
		return "Required resolution evidence is unavailable."
	case "candidate_codec_missing", "target_codec_missing":
		return "Required codec evidence is unavailable."
	case "candidate_container_missing", "target_container_missing":
		return "Required container evidence is unavailable."
	case "candidate_provider_missing", "target_provider_missing":
		return "Required provider evidence is unavailable."
	case "candidate_group_missing", "target_group_missing":
		return "Required release-group evidence is unavailable."
	case "candidate_edition_missing", "target_edition_missing":
		return "Required edition evidence is unavailable."
	case "candidate_region_missing", "target_region_missing":
		return "Required region evidence is unavailable."
	case "candidate_3d_missing", "target_3d_missing":
		return "Required 3D evidence is unavailable."
	case "candidate_repack_missing", "target_repack_missing":
		return "Required repack evidence is unavailable."
	case "candidate_date_missing":
		return "Candidate daily-episode date evidence is unavailable."
	case "candidate_season_missing":
		return "Candidate season evidence is unavailable."
	case "candidate_episode_missing":
		return "Candidate episode evidence is unavailable."
	case "different_daily_episode":
		return "Candidate is a different daily episode."
	case "different_season":
		return "Candidate is from a different season."
	case "different_episode":
		return "Candidate is a different episode."
	case "size_variance":
		return "Absolute size variance satisfies tracker coexistence policy."
	case "candidate_trumpable":
		return "Tracker marks the candidate as potentially trumpable."
	case "same_tracker_slot":
		return "Candidate occupies the same tracker slot and requires review."
	case "tracker_policy_not_evidence_backed":
		return "Tracker policy evidence is incomplete; manual review is required."
	case "proposed_webdl_over_webrip":
		return "Proposed WEB-DL is preferred over the existing WEBRip."
	case "existing_webdl_over_webrip":
		return "Existing WEB-DL is preferred over the proposed WEBRip."
	default:
		if strings.HasPrefix(reason, "different_") {
			return "Candidate occupies a distinct tracker slot."
		}
		return "Tracker policy requires review of this candidate."
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
			HDR:            cloneHDRFacts(candidate.HDR),
			Category:       candidate.Category,
			Type:           candidate.Type,
			Resolution:     candidate.Resolution,
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
			EvidenceStatus: candidate.HDR.Status,
		})
	}
	return result
}
