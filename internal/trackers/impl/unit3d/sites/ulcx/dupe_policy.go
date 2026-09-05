// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const ulcxDupeEvidenceID = "ulcx-upload-rules-v1.0.0"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:          "ulcx/duplicate/v3",
		EvidenceID:  ulcxDupeEvidenceID,
		SearchScope: trackers.DupeSearchScope{MaxPages: 100},
		SlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionMediaClass,
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionHDR,
		},
		CoexistenceRules: append([]trackers.DupeRule{
			ulcxWEBDLDifferentProviderRule(),
			ulcxDifferentCodecRule("web", "ulcx_webdl_codec_slot"),
			ulcxDifferentCodecRule("encode", "ulcx_encode_codec_slot"),
		}, ulcxObjectiveCoexistenceRules()...),
		PrecedenceRules: ulcxWEBDLHDRPrecedenceRules(),
		ManualReviewRules: []trackers.DupeRule{
			ulcxFullDiscReviewRule(),
			ulcxRemuxReviewRule(),
			ulcxEncodeReviewRule(),
			ulcxWEBDLSameSlotRule(),
		},
	}
}

func ulcxObjectiveCoexistenceRules() []trackers.DupeRule {
	var rules []trackers.DupeRule
	for _, dimension := range []trackers.DupeDimension{
		trackers.DupeDimensionResolution,
		trackers.DupeDimensionEdition,
		trackers.DupeDimensionRegion,
		trackers.DupeDimensionThreeD,
	} {
		rules = append(rules, ulcxDistinctVariantRule(string(dimension), trackers.DupeCondition{
			Dimension:       dimension,
			ValuesDifferent: true,
		}))
	}
	// Section 8 groups HDR10-family variants into one compatibility slot.
	// Their relative quality still needs review outside the WEB-DL rules.
	hdrSlots := []struct {
		id      string
		formats []string
	}{
		{id: "sdr", formats: []string{"sdr"}},
		{id: "hdr10", formats: []string{"pq10", "hdr10", "hdr10_plus", "dolby_vision+hdr10", "dolby_vision+hdr10_plus"}},
		{id: "hlg", formats: []string{"hlg"}},
		{id: "dv", formats: []string{"dolby_vision"}},
	}
	for _, targetSlot := range hdrSlots {
		for _, candidateSlot := range hdrSlots {
			if targetSlot.id == candidateSlot.id {
				continue
			}
			rules = append(rules, ulcxDistinctVariantRule("hdr_"+targetSlot.id+"_"+candidateSlot.id, trackers.DupeCondition{
				Dimension:        trackers.DupeDimensionHDR,
				TargetValues:     targetSlot.formats,
				CandidateValues:  candidateSlot.formats,
				RequiresComplete: true,
			}))
		}
	}
	return rules
}

func ulcxDistinctVariantRule(id string, condition trackers.DupeCondition) trackers.DupeRule {
	return trackers.DupeRule{
		ID:         "ulcx/duplicate/v3/distinct_" + id,
		EvidenceID: ulcxDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: "ulcx_distinct_" + string(condition.Dimension) + "_slot",
		// Objective slots outrank subjective review, below season-pack containment (850).
		Priority:   820,
		Conditions: []trackers.DupeCondition{condition},
	}
}

func ulcxWEBDLDifferentProviderRule() trackers.DupeRule {
	return trackers.DupeRule{
		ID:         "ulcx/duplicate/v3/webdl_provider_slots",
		EvidenceID: ulcxDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: "ulcx_webdl_provider_slot",
		Priority:   810,
		Conditions: []trackers.DupeCondition{
			ulcxSameMediaClassCondition("web"),
			{
				Dimension:        trackers.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionProvider,
				ValuesDifferent:  true,
				RequiresComplete: true,
			},
		},
	}
}

func ulcxDifferentCodecRule(mediaClass string, reason string) trackers.DupeRule {
	return trackers.DupeRule{
		ID:         "ulcx/duplicate/v3/" + mediaClass + "_codec_slots",
		EvidenceID: ulcxDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: reason,
		Priority:   810,
		Conditions: []trackers.DupeCondition{
			ulcxSameMediaClassCondition(mediaClass),
			{
				Dimension:        trackers.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionCodec,
				ValuesDifferent:  true,
				RequiresComplete: true,
			},
		},
	}
}

func ulcxWEBDLHDRPrecedenceRules() []trackers.DupeRule {
	pairs := []struct {
		preferred string
		trumped   string
		id        string
	}{
		{
			preferred: "dolby_vision+hdr10_plus",
			trumped:   "dolby_vision+hdr10",
			id:        "dv_hdr10_plus_over_dv_hdr10",
		},
		{
			preferred: "dolby_vision+hdr10_plus",
			trumped:   "hdr10_plus",
			id:        "dv_hdr10_plus_over_hdr10_plus",
		},
		{
			preferred: "dolby_vision+hdr10_plus",
			trumped:   "hdr10",
			id:        "dv_hdr10_plus_over_hdr10",
		},
		{
			preferred: "dolby_vision+hdr10",
			trumped:   "hdr10",
			id:        "dv_hdr10_over_hdr10",
		},
		{
			preferred: "hdr10_plus",
			trumped:   "hdr10",
			id:        "hdr10_plus_over_hdr10",
		},
		{
			preferred: "hdr10",
			trumped:   "pq10",
			id:        "hdr10_over_pq10",
		},
	}
	rules := make([]trackers.DupeRule, 0, len(pairs)*2)
	for _, pair := range pairs {
		rules = append(rules,
			ulcxWEBDLHDRRule("proposed_"+pair.id, pair.preferred, pair.trumped, api.DupeRelationProposedTrumps),
			ulcxWEBDLHDRRule("existing_"+pair.id, pair.trumped, pair.preferred, api.DupeRelationExistingPreferred),
		)
	}
	return rules
}

func ulcxWEBDLHDRRule(id string, targetHDR string, candidateHDR string, relation api.DupeRelation) trackers.DupeRule {
	return trackers.DupeRule{
		ID:               "ulcx/duplicate/v3/" + id,
		EvidenceID:       ulcxDupeEvidenceID,
		Relation:         string(relation),
		ReasonCode:       "ulcx_webdl_hdr_precedence",
		Priority:         810,
		OverridesGeneral: true,
		Conditions: []trackers.DupeCondition{
			ulcxSameMediaClassCondition("web"),
			{
				Dimension:        trackers.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionProvider,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionCodec,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionHDR,
				TargetValues:     []string{targetHDR},
				CandidateValues:  []string{candidateHDR},
				RequiresComplete: true,
			},
		},
	}
}

func ulcxFullDiscReviewRule() trackers.DupeRule {
	return ulcxSubjectiveReviewRule("full_disc_content_review", "full_disc", "ulcx_full_disc_content_review", false)
}

func ulcxRemuxReviewRule() trackers.DupeRule {
	return ulcxSubjectiveReviewRule("remux_colour_cut_review", "remux", "ulcx_remux_colour_cut_review", false)
}

func ulcxEncodeReviewRule() trackers.DupeRule {
	rule := ulcxSubjectiveReviewRule("encode_bitrate_cut_review", "encode", "ulcx_encode_bitrate_cut_review", true)
	rule.Conditions = append(rule.Conditions, trackers.DupeCondition{
		Dimension:        trackers.DupeDimensionCodec,
		ValuesEqual:      true,
		RequiresComplete: true,
	})
	return rule
}

func ulcxSubjectiveReviewRule(id string, mediaClass string, reason string, overridesGeneral bool) trackers.DupeRule {
	return trackers.DupeRule{
		ID:                 "ulcx/duplicate/v3/" + id,
		EvidenceID:         ulcxDupeEvidenceID,
		ReasonCode:         reason,
		RequiresManualStep: true,
		Priority:           810,
		OverridesGeneral:   overridesGeneral,
		Conditions: []trackers.DupeCondition{
			ulcxSameMediaClassCondition(mediaClass),
			{
				Dimension:        trackers.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
		},
	}
}

func ulcxWEBDLSameSlotRule() trackers.DupeRule {
	return trackers.DupeRule{
		ID:               "ulcx/duplicate/v3/webdl_same_slot",
		EvidenceID:       ulcxDupeEvidenceID,
		Relation:         string(api.DupeRelationSameSlot),
		ReasonCode:       "ulcx_webdl_same_slot",
		Priority:         810,
		OverridesGeneral: true,
		Conditions: []trackers.DupeCondition{
			ulcxSameMediaClassCondition("web"),
			{
				Dimension:        trackers.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionProvider,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionCodec,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionHDR,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
		},
	}
}

func ulcxSameMediaClassCondition(mediaClass string) trackers.DupeCondition {
	return trackers.DupeCondition{
		Dimension:       trackers.DupeDimensionMediaClass,
		TargetValues:    []string{mediaClass},
		CandidateValues: []string{mediaClass},
		ValuesEqual:     true,
	}
}
