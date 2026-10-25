// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"net/url"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// duplicateEvidence preserves known upload facts without treating retired
// questionnaire answers as source provenance or comparison authority.
func duplicateEvidence(meta api.UploadSubject) (api.DupeReleaseEvidence, []api.DupeComparisonEvidence) {
	return api.DupeReleaseEvidence{
		PersonalRelease: meta.PersonalRelease,
		Group:           trackers.NormalizeTrackerReleaseGroup(meta.Tag),
	}, nil
}

func duplicateEvidenceLink(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

func compareDuplicateEvidence(context api.DupeEvidenceContext) api.DupeEvidenceDecision {
	target, candidate := context.Target, context.Candidate
	var proof api.DupeComparisonEvidence
	for _, comparison := range target.Comparisons {
		if comparison.CandidateID == context.CandidateID && comparison.CandidateFingerprint == context.CandidateFingerprint {
			if proof.CandidateID != "" {
				return api.DupeEvidenceDecision{Relation: api.DupeRelationManualReview, Reason: "lst_comparison_evidence_conflict"}
			}
			proof = comparison
		}
	}
	reviewed := proof.Reviewed && duplicateEvidenceLink(proof.Source) && context.CandidateID != "" &&
		proof.CandidateID == context.CandidateID && proof.CandidateFingerprint != "" && proof.CandidateFingerprint == context.CandidateFingerprint
	decision := func(relation api.DupeRelation, reason string) api.DupeEvidenceDecision {
		return api.DupeEvidenceDecision{Relation: relation, Reason: reason}
	}
	if reviewed {
		if knownInternal(candidate.Evidence.InternalStatus) && knownInternal(proof.Candidate.InternalStatus) &&
			candidate.Evidence.InternalStatus != proof.Candidate.InternalStatus {
			return decision(api.DupeRelationManualReview, "lst_internal_evidence_conflict")
		}
		originalStatus := candidate.Evidence.InternalStatus
		candidate.Evidence = proof.Candidate
		if knownInternal(originalStatus) {
			candidate.Evidence.InternalStatus = originalStatus
		}
	}
	if context.Relation == api.DupeRelationCoexists && context.Reason != "proposed_season_pack" {
		return api.DupeEvidenceDecision{}
	}
	sameSlot := context.SlotEvidenceComplete && (sameEvidenceSlot(target, candidate) ||
		target.Evidence.HDRLayerSource == "web_hybrid" && candidate.Evidence.HDRLayerSource == "web_hybrid" && hybridHDRSlot(target, candidate))
	if reviewed && target.Type == "full_disc" && candidate.Type == "full_disc" && target.Evidence.DiscIdentity != "" &&
		candidate.Evidence.DiscIdentity != "" && target.Evidence.DiscIdentity != candidate.Evidence.DiscIdentity {
		return decision(api.DupeRelationCoexists, "lst_distinct_disc_identity")
	}
	if reviewed && context.SlotEvidenceComplete && context.Reason == "lst_webrip_comparison_required" && proof.VisualImprovement &&
		proof.Quality == "superior" {
		return decision(api.DupeRelationCoexists, "lst_webrip_visual_improvement")
	}
	if candidate.Evidence.InternalStatus == "internal" {
		if reviewed && sameSlot && target.Evidence.InternalStatus == "non_internal" && proof.Quality == "superior" {
			return decision(api.DupeRelationCoexists, "lst_superior_release_alongside_internal")
		}
		if reviewed && sameSlot && target.Evidence.InternalStatus == "internal" &&
			target.Evidence.Group != "" && strings.EqualFold(target.Evidence.Group, candidate.Evidence.Group) &&
			slices.Contains([]string{"repack", "newer"}, proof.Replacement) {
			return decision(api.DupeRelationProposedTrumps, "lst_same_internal_group_replacement")
		}
		return decision(api.DupeRelationExistingPreferred, "lst_internal_protected")
	}
	if !knownInternal(candidate.Evidence.InternalStatus) &&
		(context.Relation == api.DupeRelationProposedTrumps || context.Reason == "proposed_season_pack" || target.Evidence.InternalStatus == "internal") {
		return decision(api.DupeRelationManualReview, "lst_candidate_internal_status_unknown")
	}
	if target.Type == "full_disc" && candidate.Type == "full_disc" &&
		(target.Evidence.DiscForm == "custom" || candidate.Evidence.DiscForm == "custom") {
		if target.Evidence.DiscIdentity == "" || candidate.Evidence.DiscIdentity == "" {
			return decision(api.DupeRelationManualReview, "lst_disc_identity_unverified")
		}
		if target.Evidence.DiscForm == "custom" && candidate.Evidence.DiscForm == "unmodified" {
			return decision(api.DupeRelationExistingPreferred, "lst_unmodified_disc_exists")
		}
		if target.Evidence.DiscForm == "unmodified" && candidate.Evidence.DiscForm == "custom" {
			if !context.SlotEvidenceComplete {
				return decision(api.DupeRelationManualReview, "lst_disc_slot_unverified")
			}
			if candidate.Evidence.InternalStatus != "non_internal" {
				return decision(api.DupeRelationManualReview, "lst_candidate_internal_status_unknown")
			}
			return decision(api.DupeRelationProposedTrumps, "lst_unmodified_disc_replacement")
		}
		return decision(api.DupeRelationManualReview, "lst_disc_form_unverified")
	}
	if target.Evidence.InternalStatus == "internal" && candidate.Evidence.InternalStatus == "non_internal" {
		if reviewed && sameSlot {
			if proof.Quality == "equivalent" {
				if target.Evidence.PersonalRelease && target.Evidence.BackCatalogue != "yes" {
					if proof.MeaningfulBenefit == "no" && target.Evidence.BackCatalogue == "no" {
						return decision(api.DupeRelationExistingPreferred, "lst_personal_internal_benefit_required")
					}
					if proof.MeaningfulBenefit != "yes" {
						return decision(api.DupeRelationManualReview, "lst_personal_internal_benefit_unverified")
					}
				}
				return decision(api.DupeRelationCoexists, "lst_equivalent_internal_coexists")
			}
			if proof.Quality == "superior" {
				if target.Evidence.BackCatalogue == "yes" {
					if context.Trumpable && proof.DefectImproved {
						return decision(api.DupeRelationProposedTrumps, "lst_back_catalogue_defect_improved")
					}
					return decision(api.DupeRelationCoexists, "lst_back_catalogue_coexists")
				}
				if target.Evidence.BackCatalogue == "no" {
					return decision(api.DupeRelationProposedTrumps, "lst_superior_internal_replacement")
				}
			}
		}
		return decision(api.DupeRelationManualReview, "lst_internal_comparison_required")
	}
	if target.Type == "remux" && candidate.Type == "remux" && target.Resolution == "2160p" && candidate.Resolution == "2160p" &&
		(target.Evidence.HDRLayerSource == "web_hybrid" || candidate.Evidence.HDRLayerSource == "web_hybrid") {
		if !reviewed || !context.SlotEvidenceComplete || candidate.Evidence.InternalStatus != "non_internal" ||
			(!sameSlot && !hybridHDRSlot(target, candidate)) ||
			!knownLayerSource(target.Evidence.HDRLayerSource) ||
			!knownLayerSource(candidate.Evidence.HDRLayerSource) {
			return decision(api.DupeRelationManualReview, "lst_hybrid_source_comparison_required")
		}
		if target.Evidence.HDRLayerSource == "web_hybrid" && candidate.Evidence.HDRLayerSource == "web_hybrid" && hybridHDRSlot(target, candidate) {
			targetPlus := slices.Contains(target.HDR.Formats, api.HDRFormatHDR10Plus)
			candidatePlus := slices.Contains(candidate.HDR.Formats, api.HDRFormatHDR10Plus)
			if targetPlus && !candidatePlus {
				return decision(api.DupeRelationProposedTrumps, "lst_hybrid_hdr10_plus_replacement")
			}
			if candidatePlus && !targetPlus {
				return decision(api.DupeRelationExistingPreferred, "lst_hybrid_hdr10_plus_preferred")
			}
		}
		if target.Evidence.HDRLayerSource == "native_disc" && candidate.Evidence.HDRLayerSource == "web_hybrid" {
			if proof.Quality == "inferior" {
				return decision(api.DupeRelationExistingPreferred, "lst_superior_hybrid_retained")
			}
			if proof.Quality == "superior" || proof.Quality == "equivalent" {
				return decision(api.DupeRelationProposedTrumps, "lst_native_disc_replacement")
			}
		}
		if target.Evidence.HDRLayerSource == "web_hybrid" && candidate.Evidence.HDRLayerSource == "native_disc" {
			if proof.Quality == "superior" {
				return decision(api.DupeRelationCoexists, "lst_superior_hybrid_coexists")
			}
			if proof.Quality == "inferior" || proof.Quality == "equivalent" {
				return decision(api.DupeRelationExistingPreferred, "lst_native_disc_preferred")
			}
		}
		return decision(api.DupeRelationManualReview, "lst_hybrid_source_comparison_required")
	}
	return api.DupeEvidenceDecision{}
}

func knownInternal(value string) bool    { return value == "internal" || value == "non_internal" }
func knownLayerSource(value string) bool { return value == "native_disc" || value == "web_hybrid" }

func sameEvidenceSlot(target, candidate api.TrackerDuplicateTarget) bool {
	return target.Type != "" && target.Type == candidate.Type && target.Resolution != "" && target.Resolution == candidate.Resolution &&
		target.HDR.Status == api.HDREvidenceComplete && candidate.HDR.Status == api.HDREvidenceComplete &&
		len(target.HDR.Formats) > 0 && len(target.HDR.Formats) == len(candidate.HDR.Formats) &&
		!slices.ContainsFunc(target.HDR.Formats, func(format api.HDRFormat) bool { return !slices.Contains(candidate.HDR.Formats, format) })
}

func hybridHDRSlot(target, candidate api.TrackerDuplicateTarget) bool {
	if target.Type != "remux" || candidate.Type != "remux" || target.Resolution != "2160p" || candidate.Resolution != "2160p" {
		return false
	}
	for _, facts := range []api.HDRFacts{target.HDR, candidate.HDR} {
		if facts.Status != api.HDREvidenceComplete || !slices.Contains(facts.Formats, api.HDRFormatDolbyVision) ||
			len(facts.Formats) != 2 || (!slices.Contains(facts.Formats, api.HDRFormatHDR10) && !slices.Contains(facts.Formats, api.HDRFormatHDR10Plus)) {
			return false
		}
	}
	return true
}
