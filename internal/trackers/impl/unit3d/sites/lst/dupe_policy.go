// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const lstDupeEvidenceID = "lst-upload-rules-slots-trumping"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:         "lst/duplicate/v3",
		EvidenceID: lstDupeEvidenceID,
		SearchScope: trackers.DupeSearchScope{
			MaxPages: 100,
		},
		SlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionMediaKind,
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionHDR,
		},
		OptionalSlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionProvider,
			trackers.DupeDimensionEdition,
			trackers.DupeDimensionRegion,
			trackers.DupeDimensionThreeD,
		},
		HDRPartialMode:                        trackers.DupeHDRPartialExplicitTitle,
		SlotContradictionsRequireManualReview: true,
		CoexistenceRules: append(
			lstCodecCoexistenceRules(),
			lstWEBDLAlongsideWEBRipRule(),
		),
		PrecedenceRules:   lstWEBDLHDRPrecedenceRules(),
		ManualReviewRules: append(lstSubjectiveSlotRules(), lstMasterSensitiveHDRRules()...),
	}
}

func lstCodecCoexistenceRules() []trackers.DupeRule {
	return []trackers.DupeRule{
		{
			ID:         "lst/duplicate/v3/webdl_codec_slots",
			EvidenceID: lstDupeEvidenceID,
			Relation:   string(api.DupeRelationCoexists),
			ReasonCode: "lst_webdl_codec_slot",
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"web_dl"},
					CandidateValues: []string{"web_dl"},
				},
				{
					Dimension:       trackers.DupeDimensionResolution,
					TargetValues:    []string{"1080p", "1440p", "2160p"},
					CandidateValues: []string{"1080p", "1440p", "2160p"},
					ValuesEqual:     true,
				},
				{Dimension: trackers.DupeDimensionProvider, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionCodec, ValuesDifferent: true},
			},
		},
		{
			ID:         "lst/duplicate/v3/encode_codec_slots",
			EvidenceID: lstDupeEvidenceID,
			Relation:   string(api.DupeRelationCoexists),
			ReasonCode: "lst_encode_codec_slot",
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"disc_encode", "other_encode"},
					CandidateValues: []string{"disc_encode", "other_encode"},
					ValuesEqual:     true,
				},
				{
					Dimension:       trackers.DupeDimensionResolution,
					TargetValues:    []string{"1080p", "2160p"},
					CandidateValues: []string{"1080p", "2160p"},
					ValuesEqual:     true,
				},
				{Dimension: trackers.DupeDimensionCodec, ValuesDifferent: true},
			},
		},
	}
}

func lstWEBDLAlongsideWEBRipRule() trackers.DupeRule {
	return trackers.DupeRule{
		ID:               "lst/duplicate/v3/proposed_webdl_alongside_webrip",
		EvidenceID:       lstDupeEvidenceID,
		Relation:         string(api.DupeRelationCoexists),
		ReasonCode:       "lst_webdl_retains_slot",
		Priority:         850,
		OverridesGeneral: true,
		Conditions: []trackers.DupeCondition{
			{
				Dimension:       trackers.DupeDimensionMediaKind,
				TargetValues:    []string{"web_dl"},
				CandidateValues: []string{"web_rip"},
			},
			{Dimension: trackers.DupeDimensionResolution, ValuesEqual: true},
			{Dimension: trackers.DupeDimensionProvider, ValuesEqual: true},
			{Dimension: trackers.DupeDimensionHDR, ValuesEqual: true},
		},
	}
}

func lstWEBDLHDRPrecedenceRules() []trackers.DupeRule {
	rules := make([]trackers.DupeRule, 0, 4)
	for _, pair := range []struct {
		combined       string
		base           string
		proposedRuleID string
		existingRuleID string
	}{
		{
			combined:       "dolby_vision+hdr10",
			base:           "hdr10",
			proposedRuleID: "lst/duplicate/v3/proposed_dv_hdr_over_hdr10",
			existingRuleID: "lst/duplicate/v3/existing_dv_hdr_over_hdr10",
		},
		{
			combined:       "dolby_vision+hdr10_plus",
			base:           "hdr10_plus",
			proposedRuleID: "lst/duplicate/v3/proposed_dv_hdr_over_hdr10_plus",
			existingRuleID: "lst/duplicate/v3/existing_dv_hdr_over_hdr10_plus",
		},
	} {
		rules = append(rules,
			lstWEBDLHDRRule(
				pair.proposedRuleID,
				pair.combined,
				pair.base,
				api.DupeRelationProposedTrumps,
				"lst_proposed_dv_hdr_trumps_hdr",
			),
			lstWEBDLHDRRule(
				pair.existingRuleID,
				pair.base,
				pair.combined,
				api.DupeRelationExistingPreferred,
				"lst_existing_dv_hdr_trumps_hdr",
			),
		)
	}
	return rules
}

func lstWEBDLHDRRule(id string, targetHDR string, candidateHDR string, relation api.DupeRelation, reason string) trackers.DupeRule {
	return trackers.DupeRule{
		ID:               id,
		EvidenceID:       lstDupeEvidenceID,
		Relation:         string(relation),
		ReasonCode:       reason,
		Priority:         850,
		OverridesGeneral: true,
		Conditions: []trackers.DupeCondition{
			{
				Dimension:       trackers.DupeDimensionMediaKind,
				TargetValues:    []string{"web_dl"},
				CandidateValues: []string{"web_dl"},
			},
			{
				Dimension:       trackers.DupeDimensionResolution,
				TargetValues:    []string{"1080p", "1440p", "2160p"},
				CandidateValues: []string{"1080p", "1440p", "2160p"},
				ValuesEqual:     true,
			},
			{
				Dimension:       trackers.DupeDimensionCodec,
				TargetValues:    []string{"h265"},
				CandidateValues: []string{"h265"},
			},
			{Dimension: trackers.DupeDimensionProvider, ValuesEqual: true},
			{
				Dimension:       trackers.DupeDimensionHDR,
				TargetValues:    []string{targetHDR},
				CandidateValues: []string{candidateHDR},
			},
		},
	}
}

func lstSubjectiveSlotRules() []trackers.DupeRule {
	return []trackers.DupeRule{
		{
			ID:                 "lst/duplicate/v3/full_disc_content_review",
			EvidenceID:         lstDupeEvidenceID,
			ReasonCode:         "lst_full_disc_content_review",
			RequiresManualStep: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"full_disc"},
					CandidateValues: []string{"full_disc"},
				},
				{Dimension: trackers.DupeDimensionResolution, ValuesEqual: true},
			},
		},
		{
			ID:                 "lst/duplicate/v3/encode_quality_or_master_review",
			EvidenceID:         lstDupeEvidenceID,
			ReasonCode:         "lst_encode_quality_or_master_review",
			RequiresManualStep: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"disc_encode", "other_encode"},
					CandidateValues: []string{"disc_encode", "other_encode"},
					ValuesEqual:     true,
				},
				{Dimension: trackers.DupeDimensionResolution, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionCodec, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionHDR, ValuesEqual: true},
			},
		},
		{
			ID:                 "lst/duplicate/v3/remux_master_review",
			EvidenceID:         lstDupeEvidenceID,
			ReasonCode:         "lst_remux_master_review",
			RequiresManualStep: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"remux"},
					CandidateValues: []string{"remux"},
				},
				{Dimension: trackers.DupeDimensionResolution, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionHDR, ValuesEqual: true},
			},
		},
		{
			ID:                 "lst/duplicate/v3/proposed_webrip_requires_comparison",
			EvidenceID:         lstDupeEvidenceID,
			ReasonCode:         "lst_webrip_comparison_required",
			RequiresManualStep: true,
			Priority:           850,
			OverridesGeneral:   true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"web_rip"},
					CandidateValues: []string{"web_dl"},
				},
				{Dimension: trackers.DupeDimensionResolution, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionProvider, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionHDR, ValuesEqual: true},
			},
		},
		{
			ID:                 "lst/duplicate/v3/webrip_quality_review",
			EvidenceID:         lstDupeEvidenceID,
			ReasonCode:         "lst_webrip_quality_review",
			RequiresManualStep: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"web_rip"},
					CandidateValues: []string{"web_rip"},
				},
				{Dimension: trackers.DupeDimensionResolution, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionProvider, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionHDR, ValuesEqual: true},
			},
		},
		{
			ID:                 "lst/duplicate/v3/2160p_webdl_lossless_audio_review",
			EvidenceID:         lstDupeEvidenceID,
			ReasonCode:         "lst_2160p_webdl_lossless_audio_review",
			RequiresManualStep: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"web_dl"},
					CandidateValues: []string{"web_dl"},
				},
				{
					Dimension:       trackers.DupeDimensionResolution,
					TargetValues:    []string{"2160p"},
					CandidateValues: []string{"2160p"},
				},
				{Dimension: trackers.DupeDimensionProvider, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionCodec, ValuesEqual: true},
				{Dimension: trackers.DupeDimensionHDR, ValuesEqual: true},
			},
		},
	}
}

func lstMasterSensitiveHDRRules() []trackers.DupeRule {
	rules := make([]trackers.DupeRule, 0, 8)
	for _, direction := range []struct {
		targetHDR    string
		candidateHDR string
		encodeRuleID string
		remuxRuleID  string
	}{
		{
			targetHDR:    "dolby_vision+hdr10",
			candidateHDR: "hdr10",
			encodeRuleID: "lst/duplicate/v3/encode_proposed_dv_hdr_over_hdr10_review",
			remuxRuleID:  "lst/duplicate/v3/remux_proposed_dv_hdr_over_hdr10_review",
		},
		{
			targetHDR:    "hdr10",
			candidateHDR: "dolby_vision+hdr10",
			encodeRuleID: "lst/duplicate/v3/encode_existing_dv_hdr_over_hdr10_review",
			remuxRuleID:  "lst/duplicate/v3/remux_existing_dv_hdr_over_hdr10_review",
		},
		{
			targetHDR:    "dolby_vision+hdr10_plus",
			candidateHDR: "hdr10_plus",
			encodeRuleID: "lst/duplicate/v3/encode_proposed_dv_hdr_over_hdr10_plus_review",
			remuxRuleID:  "lst/duplicate/v3/remux_proposed_dv_hdr_over_hdr10_plus_review",
		},
		{
			targetHDR:    "hdr10_plus",
			candidateHDR: "dolby_vision+hdr10_plus",
			encodeRuleID: "lst/duplicate/v3/encode_existing_dv_hdr_over_hdr10_plus_review",
			remuxRuleID:  "lst/duplicate/v3/remux_existing_dv_hdr_over_hdr10_plus_review",
		},
	} {
		rules = append(rules,
			lstMasterSensitiveHDRRule(
				direction.encodeRuleID,
				direction.targetHDR,
				direction.candidateHDR,
				[]string{"disc_encode", "other_encode"},
				[]string{"1080p", "2160p"},
				true,
			),
			lstMasterSensitiveHDRRule(
				direction.remuxRuleID,
				direction.targetHDR,
				direction.candidateHDR,
				[]string{"remux"},
				[]string{"2160p"},
				false,
			),
		)
	}
	return rules
}

func lstMasterSensitiveHDRRule(
	id string,
	targetHDR string,
	candidateHDR string,
	mediaKinds []string,
	resolutions []string,
	requireCodec bool,
) trackers.DupeRule {
	conditions := []trackers.DupeCondition{
		{
			Dimension:       trackers.DupeDimensionMediaKind,
			TargetValues:    mediaKinds,
			CandidateValues: mediaKinds,
			ValuesEqual:     true,
		},
		{
			Dimension:       trackers.DupeDimensionResolution,
			TargetValues:    resolutions,
			CandidateValues: resolutions,
			ValuesEqual:     true,
		},
	}
	if requireCodec {
		conditions = append(conditions, trackers.DupeCondition{
			Dimension:       trackers.DupeDimensionCodec,
			TargetValues:    []string{"h265", "av1"},
			CandidateValues: []string{"h265", "av1"},
			ValuesEqual:     true,
		})
	}
	conditions = append(conditions, trackers.DupeCondition{
		Dimension:       trackers.DupeDimensionHDR,
		TargetValues:    []string{targetHDR},
		CandidateValues: []string{candidateHDR},
	})
	return trackers.DupeRule{
		ID:                 id,
		EvidenceID:         lstDupeEvidenceID,
		ReasonCode:         "lst_hdr_trump_requires_master_review",
		RequiresManualStep: true,
		Priority:           850,
		OverridesGeneral:   true,
		Conditions:         conditions,
	}
}
