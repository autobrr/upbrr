// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const dupeEvidenceID = "aither-slots-trumping-669"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:          "aither/duplicate/v3",
		EvidenceID:  dupeEvidenceID,
		SearchScope: trackers.DupeSearchScope{MaxPages: 100},
		Slots:       ordinarySlots(),
		SuppressGeneralCoexistence: []trackers.DupeDimension{
			trackers.DupeDimensionType,
			trackers.DupeDimensionMediaKind,
			trackers.DupeDimensionMediaClass,
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionHDR,
		},
		SlotContradictionsRequireManualReview: true,
		TargetReviewRules: []trackers.DupeTargetReview{
			{
				ID:                   "aither/pack_scope_review",
				IncludeIndeterminate: true,
				Predicates: []trackers.DupeSetPredicate{
					completePredicate(trackers.DupeDimensionPack, "true"),
					completePredicate(trackers.DupeDimensionSeason, "0"),
				},
				Reason: "Specials must be individual episodes. Verify a single ordinary season before using season-pack slots; S00 and unresolved collection scopes do not establish an ordinary pack slot.",
			},
			{
				ID:         "aither/open_matte_approval",
				Predicates: []trackers.DupeSetPredicate{{Dimension: trackers.DupeDimensionEdition, ValueTokens: []string{"open_matte"}}},
				Reason:     "Open Matte uploads require staff approval; title wording is not approval.",
			},
			{
				ID: "aither/webrip_comparisons",
				Predicates: []trackers.DupeSetPredicate{
					completePredicate(trackers.DupeDimensionType, "WEBRIP", "WEB-RIP"),
					completePredicate(trackers.DupeDimensionMediaKind, "web_rip"),
				},
				Reason: "WEBRip eligibility and comparison screenshots require review.",
			},
			{
				ID: "aither/web_encode_comparisons",
				Predicates: []trackers.DupeSetPredicate{
					completePredicate(trackers.DupeDimensionType, "ENCODE"),
					{Dimension: trackers.DupeDimensionSource, Values: []string{"web"}},
				},
				Reason: "WEB-source encodes require source-resolution and Blu-ray superiority evidence.",
			},
		},
		PrecedenceRules:   precedenceRules(),
		ManualReviewRules: []trackers.DupeRule{differentX264HDRRule()},
		SetRules:          []trackers.DupeSetRule{encodeCapacityRule()},
	}
}

// ordinarySlots lists only documented families. Availability and provenance
// prerequisites remain reviewable because search results cannot establish them.
func ordinarySlots() []trackers.DupeSlot {
	var slots []trackers.DupeSlot
	add := func(id, resolution, kind, codec, hdr, review string) {
		kinds := []string{kind}
		if kind == "web" {
			kinds = []string{"web_dl", "web_rip"}
		}
		resolutions := []string{resolution}
		if resolution == "sd" {
			resolutions = []string{"sd", "480p", "480i", "576p", "576i"}
		}
		predicates := []trackers.DupeSetPredicate{
			completePredicate(trackers.DupeDimensionType),
			completePredicate(trackers.DupeDimensionResolution, resolutions...),
		}
		if kind == "encode" {
			predicates[0].Values = []string{"ENCODE"}
		} else {
			predicates = append(predicates, completePredicate(trackers.DupeDimensionMediaKind, kinds...))
		}
		if kind == "web" {
			predicates[0].ExcludedValues = []string{"ENCODE"}
		}
		if codec != "" {
			predicates = append(predicates, completePredicate(trackers.DupeDimensionCodec, codec))
		}
		if hdr != "" {
			formats := []string{hdr}
			if hdr == "hdr10" || hdr == "hdr10_plus" {
				formats = append(formats, "dolby_vision+"+hdr)
			}
			predicates = append(predicates, completePredicate(trackers.DupeDimensionHDR, formats...))
		} else if kind != "full_disc" {
			predicates = append(predicates, completePredicate(trackers.DupeDimensionHDR))
		}
		if id == "1080p_encode_h264" {
			predicates = append(predicates, completePredicate(trackers.DupeDimensionSize))
		}
		if resolution == "sd" && kind == "remux" {
			predicates = append(predicates, completePredicate(trackers.DupeDimensionSource, "dvd"))
		}
		slots = append(slots, trackers.DupeSlot{
			ID:           id,
			Predicates:   predicates,
			ReviewReason: review,
		})
	}
	const sdReview = "SD uploads require evidence that no equivalent HD retail source is available."
	add("sd_web", "sd", "web", "", "", sdReview)
	add("sd_dvdrip", "sd", "dvd_rip", "", "", sdReview)
	add("sd_remux", "sd", "remux", "", "", sdReview)
	add("720p_web_h264", "720p", "web", "h264", "", "")
	add("720p_encode_h264", "720p", "encode", "h264", "", "")
	for _, resolution := range []string{"1080p", "1440p"} {
		review := ""
		if resolution == "1440p" {
			review = "1440p uploads require evidence that no 2160p source is available."
		}
		add(resolution+"_web_h264", resolution, "web", "h264", "", review)
		add(resolution+"_web_dv", resolution, "web", "h265", "dolby_vision", review)
		add(resolution+"_web_hdr", resolution, "web", "h265", "hdr10", review)
	}
	add("1080p_encode_h264", "1080p", "encode", "h264", "", "")
	add("1080p_encode_h265_sdr", "1080p", "encode", "h265", "sdr", "")
	add("1080p_encode_h265_hdr", "1080p", "encode", "h265", "hdr10", "")
	add("1080p_remux", "1080p", "remux", "", "", "")
	for _, kind := range []string{"web", "encode", "remux"} {
		codec := "h265"
		if kind == "remux" {
			codec = ""
		}
		for _, hdr := range []string{"sdr", "hdr10", "hdr10_plus"} {
			review := ""
			if hdr == "hdr10_plus" && kind != "web" {
				review = "HDR10+ encode and REMUX uploads require retail/hybrid provenance and applicable grade checks, plots, and superiority evidence."
			}
			add("2160p_"+kind+"_"+hdr, "2160p", kind, codec, hdr, review)
		}
	}
	add("2160p_web_dv", "2160p", "web", "h265", "dolby_vision", "")
	for _, resolution := range []string{"sd", "1080p", "2160p"} {
		add(resolution+"_full_disc", resolution, "full_disc", "", "", "")
	}
	return slots
}

func completePredicate(dimension trackers.DupeDimension, values ...string) trackers.DupeSetPredicate {
	return trackers.DupeSetPredicate{
		Dimension:        dimension,
		Values:           values,
		RequiresComplete: true,
	}
}

func encodeCapacityRule() trackers.DupeSetRule {
	predicates := []trackers.DupeSetPredicate{
		completePredicate(trackers.DupeDimensionType, "ENCODE"),
		completePredicate(trackers.DupeDimensionResolution, "1080p"),
		completePredicate(trackers.DupeDimensionCodec, "h264"),
		completePredicate(trackers.DupeDimensionHDR),
		completePredicate(trackers.DupeDimensionSize),
	}
	candidates := append([]trackers.DupeSetPredicate(nil), predicates...)

	for _, dimension := range []trackers.DupeDimension{
		trackers.DupeDimensionEdition, trackers.DupeDimensionRegion, trackers.DupeDimensionThreeD,
		trackers.DupeDimensionPack, trackers.DupeDimensionSeason, trackers.DupeDimensionEpisode, trackers.DupeDimensionDate,
	} {
		candidates = append(candidates, trackers.DupeSetPredicate{
			Dimension:        dimension,
			MatchTarget:      true,
			Optional:         true,
			RequiresComplete: true,
		})
	}
	return trackers.DupeSetRule{
		ID:                           "aither/duplicate/v3/1080p_x264_capacity",
		EvidenceID:                   dupeEvidenceID,
		TargetPredicates:             predicates,
		CandidatePredicates:          candidates,
		Capacity:                     2,
		MinimumSizeSeparationPercent: 20,
		RequireSameContent:           true,
	}
}

func precedenceRules() []trackers.DupeRule {
	rules := trackers.DirectionalMediaKindRules(dupeEvidenceID, "web_dl", "web_rip")
	for index := range rules {
		rules[index].ID = "aither/duplicate/v3/" + rules[index].ID
		rules[index].ReasonCode = "aither_same_source_web_precedence"
		rules[index].Conditions[0].RequiresComplete = true
		rules[index].Conditions = append(rules[index].Conditions, trackers.DupeCondition{
			Dimension:        trackers.DupeDimensionType,
			TargetValues:     []string{"WEBDL", "WEB-DL", "WEBRIP", "WEB-RIP"},
			CandidateValues:  []string{"WEBDL", "WEB-DL", "WEBRIP", "WEB-RIP"},
			RequiresComplete: true,
		})
		for _, dimension := range []trackers.DupeDimension{trackers.DupeDimensionSourceFamily, trackers.DupeDimensionProvider, trackers.DupeDimensionCodec, trackers.DupeDimensionHDR} {
			rules[index].Conditions = append(rules[index].Conditions, equalCondition(dimension))
		}
	}
	for _, slot := range ordinarySlots() {
		var hdr string
		for _, predicate := range slot.Predicates {
			if predicate.Dimension == trackers.DupeDimensionHDR && len(predicate.Values) == 2 {
				hdr = predicate.Values[0]
			}
		}
		if hdr == "" {
			continue
		}
		for _, proposed := range []bool{true, false} {
			target, candidate := "dolby_vision+"+hdr, hdr
			relation := api.DupeRelationProposedTrumps
			if !proposed {
				target, candidate = candidate, target
				relation = api.DupeRelationExistingPreferred
			}
			conditions := []trackers.DupeCondition{{
				Dimension:        trackers.DupeDimensionHDR,
				TargetValues:     []string{target},
				CandidateValues:  []string{candidate},
				RequiresComplete: true,
			}}
			for _, predicate := range slot.Predicates {
				if predicate.Dimension == trackers.DupeDimensionHDR {
					continue
				}
				conditions = append(conditions, trackers.DupeCondition{
					Dimension:        predicate.Dimension,
					TargetValues:     predicate.Values,
					CandidateValues:  predicate.Values,
					RequiresComplete: true,
				})
			}
			for _, dimension := range []trackers.DupeDimension{trackers.DupeDimensionType, trackers.DupeDimensionMediaClass, trackers.DupeDimensionResolution, trackers.DupeDimensionCodec} {
				conditions = append(conditions, equalCondition(dimension))
			}
			rules = append(rules, trackers.DupeRule{
				ID:                 "aither/duplicate/v3/" + string(relation) + "_" + slot.ID,
				EvidenceID:         dupeEvidenceID,
				Relation:           string(relation),
				ReasonCode:         "aither_hdr_compatibility_precedence",
				OverridesGeneral:   true,
				Conditions:         conditions,
				RequiresManualStep: hdr == "hdr10_plus" && !strings.Contains(slot.ID, "_web_"),
			})
		}
	}

	for index := range rules {
		for _, dimension := range []trackers.DupeDimension{trackers.DupeDimensionEdition, trackers.DupeDimensionRegion, trackers.DupeDimensionThreeD} {
			condition := equalCondition(dimension)
			condition.Optional = true
			rules[index].Conditions = append(rules[index].Conditions, condition)
		}
	}
	return rules
}

func equalCondition(dimension trackers.DupeDimension) trackers.DupeCondition {
	return trackers.DupeCondition{
		Dimension:        dimension,
		ValuesEqual:      true,
		RequiresComplete: true,
	}
}

// Different HDR presentations share the bounded x264 family, but size cannot
// establish their comparative compatibility or quality.
func differentX264HDRRule() trackers.DupeRule {
	conditions := []trackers.DupeCondition{{
		Dimension:        trackers.DupeDimensionHDR,
		ValuesDifferent:  true,
		RequiresComplete: true,
	}}
	for _, predicate := range encodeCapacityRule().TargetPredicates {
		if predicate.Dimension == trackers.DupeDimensionHDR || predicate.Dimension == trackers.DupeDimensionSize {
			continue
		}
		conditions = append(conditions, trackers.DupeCondition{
			Dimension:        predicate.Dimension,
			TargetValues:     predicate.Values,
			CandidateValues:  predicate.Values,
			RequiresComplete: true,
		})
	}
	for _, dimension := range []trackers.DupeDimension{trackers.DupeDimensionEdition, trackers.DupeDimensionRegion, trackers.DupeDimensionThreeD} {
		condition := equalCondition(dimension)
		condition.Optional = true
		conditions = append(conditions, condition)
	}

	return trackers.DupeRule{
		ID:               "aither/duplicate/v3/x264_hdr_review",
		EvidenceID:       dupeEvidenceID,
		Conditions:       conditions,
		Relation:         string(api.DupeRelationManualReview),
		ReasonCode:       "aither_x264_hdr_comparison",
		OverridesGeneral: true,
	}
}
