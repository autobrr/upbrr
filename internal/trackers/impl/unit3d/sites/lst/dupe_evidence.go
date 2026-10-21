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

func duplicateEvidenceKey(subject api.TrackerValidationSubject, key string) string {
	dependencies := make([]string, 0, 17)
	if key == "source_reviewed" {
		dependencies = append(dependencies, "source", "internal_status", "back_catalogue", "layer_source", "disc_identity", "disc_form")
	} else if strings.HasPrefix(key, "candidate_") && strings.HasSuffix(key, "_comparison_reviewed") {
		prefix := strings.TrimSuffix(key, "comparison_reviewed")
		dependencies = append(dependencies, "source", "internal_status", "back_catalogue", "layer_source", "disc_identity", "disc_form")
		for _, suffix := range []string{"comparison_source", "candidate_internal_status", "candidate_group", "candidate_layer_source", "candidate_disc_identity", "candidate_disc_form", "quality", "visual_improvement", "meaningful_benefit", "defect_improved", "replacement"} {
			dependencies = append(dependencies, prefix+suffix)
		}
	}
	if len(dependencies) > 0 {
		values := make([]string, 0, len(dependencies))
		for _, dependency := range dependencies {
			values = append(values, subject.QuestionnaireAnswers[duplicateEvidenceKey(subject, dependency)])
		}
		hash, _ := api.CanonicalWorkflowFingerprint(values)
		key += "_" + string(hash)
	}

	return trackers.LanguageQuestionKey(subject, "duplicate_"+key+"_"+trackers.NormalizeTrackerReleaseGroup(subject.Tag))
}

func duplicateEvidence(meta api.UploadSubject) (api.DupeReleaseEvidence, []api.DupeComparisonEvidence) {
	subject := api.NewTrackerValidationSubject(meta, "LST")
	answer := func(key string) string {
		return strings.TrimSpace(subject.QuestionnaireAnswers[duplicateEvidenceKey(subject, key)])
	}
	var evidence api.DupeReleaseEvidence
	if answer("source_reviewed") == "yes" && duplicateEvidenceLink(answer("source")) {
		evidence = api.DupeReleaseEvidence{
			PersonalRelease: meta.PersonalRelease,
			InternalStatus:  answer("internal_status"),
			Group:           trackers.NormalizeTrackerReleaseGroup(meta.Tag),
			BackCatalogue:   answer("back_catalogue"),
			HDRLayerSource:  answer("layer_source"),
			DiscIdentity:    answer("disc_identity"),
			DiscForm:        answer("disc_form"),
		}
	}
	if evidence.InternalStatus == "internal" && evidence.Group == "" {
		evidence.InternalStatus = ""
	}
	candidates := meta.TrackerDuplicateReviewCandidates["LST"]
	var comparisons []api.DupeComparisonEvidence
	if len(candidates) > 0 {
		comparisons = make([]api.DupeComparisonEvidence, 0, len(candidates))
	}
	for _, candidate := range candidates {
		candidateAnswer := func(key string) string { return answer(duplicateCandidatePrefix(candidate) + key) }
		comparison := api.DupeComparisonEvidence{
			CandidateID:          candidate.ID,
			CandidateFingerprint: candidate.EvidenceFingerprint,
			Candidate: api.DupeReleaseEvidence{
				InternalStatus: candidateAnswer("candidate_internal_status"),
				Group:          candidateAnswer("candidate_group"),
				HDRLayerSource: candidateAnswer(
					"candidate_layer_source",
				),
				DiscIdentity: candidateAnswer("candidate_disc_identity"),
				DiscForm:     candidateAnswer("candidate_disc_form"),
			},
			VisualImprovement: candidateAnswer(
				"visual_improvement",
			) == "yes",
			MeaningfulBenefit: candidateAnswer("meaningful_benefit"),
			Quality:           candidateAnswer("quality"),
			DefectImproved:    candidateAnswer("defect_improved") == "yes",
			Replacement:       candidateAnswer("replacement"),
			Reviewed:          candidateAnswer("comparison_reviewed") == "yes",
			Source:            candidateAnswer("comparison_source"),
		}
		if !duplicateEvidenceLink(comparison.Source) {
			comparison.Reviewed = false
		}
		comparisons = append(comparisons, comparison)
	}
	return evidence, comparisons
}

func duplicateEvidenceLink(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

func duplicateEvidenceQuestionnaire(input trackers.PreparationInput) []api.TrackerQuestionnaireField {
	candidates := duplicateReviewCandidates(input.Meta)
	if len(candidates) == 0 {
		return nil
	}
	subject := api.NewTrackerValidationSubject(input.Meta, "LST")
	fields := []api.TrackerQuestionnaireField{
		{
			Key:   "source",
			Label: "LST source evidence link",
			Kind:  "text",
		},
		{
			Key:     "source_reviewed",
			Label:   "I reviewed the linked source facts and site-recognized internal membership",
			Kind:    "select",
			Options: []string{"unknown", "yes"},
		},
		{
			Key:     "internal_status",
			Label:   "Verified release-group status",
			Kind:    "select",
			Options: []string{"unknown", "internal", "non_internal"},
		},
		{
			Key:     "back_catalogue",
			Label:   "Internal back catalogue",
			Kind:    "select",
			Options: []string{"unknown", "yes", "no"},
		},
	}
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:   "disc_identity",
				Label: "Verified physical disc identity",
				Kind:  "text",
				Help:  "Identify the exact disc, including edition, region and content; a movie title is insufficient.",
			},
			api.TrackerQuestionnaireField{
				Key:     "disc_form",
				Label:   "Verified disc form",
				Kind:    "select",
				Options: []string{"unknown", "custom", "unmodified"},
			},
		)
	}
	if strings.EqualFold(subject.Type, "REMUX") {
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:     "layer_source",
				Label:   "Verified HDR layer source",
				Kind:    "select",
				Options: []string{"unknown", "native_disc", "web_hybrid"},
			},
		)
	}
	for _, candidate := range candidates {
		start := len(fields)
		fields = append(
			fields,
			api.TrackerQuestionnaireField{
				Key:   "comparison_source",
				Label: "Source-linked comparison artifact",
				Kind:  "text",
			},
			api.TrackerQuestionnaireField{
				Key:     "comparison_reviewed",
				Label:   "I reviewed this exact candidate and the linked comparison",
				Kind:    "select",
				Options: []string{"unknown", "yes"},
			},
			api.TrackerQuestionnaireField{
				Key:     "candidate_internal_status",
				Label:   "Candidate verified group status",
				Kind:    "select",
				Options: []string{"unknown", "internal", "non_internal"},
			},
			api.TrackerQuestionnaireField{
				Key:   "candidate_group",
				Label: "Candidate verified release group",
				Kind:  "text",
			},
			api.TrackerQuestionnaireField{
				Key:     "quality",
				Label:   "Proposed release compared with this candidate",
				Kind:    "select",
				Options: []string{"unknown", "equivalent", "superior", "inferior"},
			},
			api.TrackerQuestionnaireField{
				Key:     "visual_improvement",
				Label:   "Comparison demonstrates clear visual improvement",
				Kind:    "select",
				Options: []string{"unknown", "yes", "no"},
			},
			api.TrackerQuestionnaireField{
				Key:     "meaningful_benefit",
				Label:   "Comparison establishes a meaningful benefit",
				Kind:    "select",
				Options: []string{"unknown", "yes", "no"},
			},
			api.TrackerQuestionnaireField{
				Key:     "defect_improved",
				Label:   "Comparison improves the candidate's documented Trumpable defect",
				Kind:    "select",
				Options: []string{"unknown", "yes", "no"},
			},
			api.TrackerQuestionnaireField{
				Key:     "replacement",
				Label:   "Verified same-group replacement relationship",
				Kind:    "select",
				Options: []string{"unknown", "repack", "newer"},
			},
		)
		if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
			fields = append(
				fields,
				api.TrackerQuestionnaireField{
					Key:   "candidate_disc_identity",
					Label: "Candidate verified physical disc identity",
					Kind:  "text",
					Help:  "Identify the exact disc, including edition, region and content.",
				},
				api.TrackerQuestionnaireField{
					Key:     "candidate_disc_form",
					Label:   "Candidate verified disc form",
					Kind:    "select",
					Options: []string{"unknown", "custom", "unmodified"},
				},
			)
		}
		if strings.EqualFold(subject.Type, "REMUX") {
			fields = append(
				fields,
				api.TrackerQuestionnaireField{
					Key:     "candidate_layer_source",
					Label:   "Candidate verified HDR layer source",
					Kind:    "select",
					Options: []string{"unknown", "native_disc", "web_hybrid"},
				},
			)
		}
		fields = append(fields[:start], slices.DeleteFunc(fields[start:], func(field api.TrackerQuestionnaireField) bool {
			switch field.Key {
			case "visual_improvement":
				return !strings.Contains(candidate.Reason, "webrip")
			case "meaningful_benefit", "defect_improved", "replacement":
				return !strings.Contains(candidate.Reason, "internal") && !strings.Contains(candidate.Reason, "back_catalogue")
			}
			return false
		})...)
		for index := start; index < len(fields); index++ {
			fields[index].Key = duplicateCandidatePrefix(candidate) + fields[index].Key
			fields[index].Label = "Candidate " + candidate.Name + ": " + fields[index].Label
		}
	}
	for i := range fields {
		fields[i].Key = duplicateEvidenceKey(subject, fields[i].Key)
		value := strings.TrimSpace(subject.QuestionnaireAnswers[fields[i].Key])
		if len(fields[i].Options) == 0 || slices.Contains(fields[i].Options, value) {
			fields[i].Value = value
		}
	}
	return fields
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

// duplicateReviewCandidates excludes exact identities, established coexistence and unrelated findings.
func duplicateReviewCandidates(meta api.UploadSubject) []api.DupeMatchProjection {
	var result []api.DupeMatchProjection
	for _, candidate := range meta.TrackerDuplicateReviewCandidates["LST"] {
		if candidate.ID == "" || candidate.EvidenceFingerprint == "" || candidate.Relation == api.DupeRelationExactDuplicate ||
			candidate.Relation == api.DupeRelationCoexists || candidate.Relation == api.DupeRelationProposedTrumps || candidate.Reason == "in_client" ||
			(!strings.HasPrefix(candidate.Reason, "lst_") && candidate.Relation != api.DupeRelationSameSlot && !candidate.Internal) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

// The key carries opaque candidate binding; users answer facts rather than copying identifiers.
func duplicateCandidatePrefix(candidate api.DupeMatchProjection) string {
	binding, _ := api.CanonicalWorkflowFingerprint([]string{candidate.ID, string(candidate.EvidenceFingerprint)})
	return "candidate_" + string(binding) + "_"
}
