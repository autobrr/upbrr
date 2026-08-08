// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const btnDupeEvidenceID = "btn-upload-rules-sha256-ceffed934da279bf7bd3dc04ab86cbc13bbcca9618ad2c415369e059df603f6f"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:                  "standalone/btn/duplicate/v1",
		EvidenceID:          btnDupeEvidenceID,
		TargetReleaseOrigin: resolveOrigin,
		SearchScope: trackers.DupeSearchScope{
			MaxPages: 100,
		},
		SlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionResolution,
		},
		CoexistenceRules: []trackers.DupeRule{
			btnDifferentValueRule("scene_p2p", trackers.DupeDimensionReleaseOrigin, "scene", "p2p"),
			btnWEBCodecRule(),
			btnDifferentValueRule("bluray_dvd", trackers.DupeDimensionSource, "bluray", "dvd"),
			btnPALNTSC(),
		},
		PrecedenceRules: btnCodecPrecedenceRules(),
		ManualReviewRules: []trackers.DupeRule{
			btnMixedOriginRule(true),
			btnMixedOriginRule(false),
		},
		SetRules: btnSetRules(),
	}
}

func btnPALNTSC() trackers.DupeRule {
	rule := btnDifferentValueRule("pal_ntsc", trackers.DupeDimensionRegion, "pal", "ntsc")
	rule.Conditions[0].RequiresComplete = false
	rule.Conditions = append(rule.Conditions, trackers.DupeCondition{
		Dimension:        trackers.DupeDimensionSource,
		TargetValues:     []string{"dvd"},
		CandidateValues:  []string{"dvd"},
		RequiresComplete: true,
	})
	return rule
}

func btnDifferentValueRule(id string, dimension trackers.DupeDimension, left string, right string) trackers.DupeRule {
	return trackers.DupeRule{
		ID:         "standalone/btn/duplicate/v1/" + id,
		EvidenceID: btnDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: "btn_" + id + "_coexists",
		Conditions: []trackers.DupeCondition{{
			Dimension:        dimension,
			TargetValues:     []string{left, right},
			CandidateValues:  []string{left, right},
			ValuesDifferent:  true,
			RequiresComplete: true,
		}},
	}
}

func btnWEBCodecRule() trackers.DupeRule {
	rule := btnDifferentValueRule("web_h265_h264", trackers.DupeDimensionCodec, "h265", "h264")
	rule.Conditions = append(rule.Conditions, trackers.DupeCondition{
		Dimension:        trackers.DupeDimensionSource,
		TargetValues:     []string{"web"},
		CandidateValues:  []string{"web"},
		RequiresComplete: true,
	})
	return rule
}

func btnCodecPrecedenceRules() []trackers.DupeRule {
	conditions := func(targetCodec string, candidateCodec string) []trackers.DupeCondition {
		return []trackers.DupeCondition{
			{
				Dimension:        trackers.DupeDimensionCodec,
				TargetValues:     []string{targetCodec},
				CandidateValues:  []string{candidateCodec},
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionSource,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
			{
				Dimension:        trackers.DupeDimensionResolution,
				ValuesEqual:      true,
				RequiresComplete: true,
			},
		}
	}
	return []trackers.DupeRule{
		{
			ID:               "standalone/btn/duplicate/v1/proposed_h264_over_xvid",
			EvidenceID:       btnDupeEvidenceID,
			Conditions:       conditions("h264", "xvid"),
			Relation:         string(api.DupeRelationProposedTrumps),
			ReasonCode:       "btn_proposed_h264_over_xvid",
			OverridesGeneral: true,
		},
		{
			ID:               "standalone/btn/duplicate/v1/existing_h264_over_xvid",
			EvidenceID:       btnDupeEvidenceID,
			Conditions:       conditions("xvid", "h264"),
			Relation:         string(api.DupeRelationExistingPreferred),
			ReasonCode:       "btn_existing_h264_over_xvid",
			OverridesGeneral: true,
		},
	}
}

func btnMixedOriginRule(proposed bool) trackers.DupeRule {
	conditions := []trackers.DupeCondition{{
		Dimension:    trackers.DupeDimensionReleaseOrigin,
		TargetValues: []string{"mixed"},
	}, {
		Dimension:    trackers.DupeDimensionPack,
		TargetValues: []string{"true"},
	}}
	id := "proposed_mixed_pack"
	if !proposed {
		conditions[0].TargetValues = nil
		conditions[0].CandidateValues = []string{"mixed"}
		conditions[1].TargetValues = nil
		conditions[1].CandidateValues = []string{"true"}
		id = "existing_mixed_pack"
	}
	return trackers.DupeRule{
		ID:                 "standalone/btn/duplicate/v1/" + id,
		EvidenceID:         btnDupeEvidenceID,
		Conditions:         conditions,
		ReasonCode:         "btn_" + id + "_manual",
		RequiresManualStep: true,
	}
}

func btnSetRules() []trackers.DupeSetRule {
	return []trackers.DupeSetRule{
		btnSeasonPackSetRule("scene_season_pack_capacity", "scene", 1),
		btnSeasonPackSetRule("p2p_season_pack_capacity", "p2p", 2),
		btnWEBSetRule("web_hd_capacity", []string{"720p", "1080p"}, 2),
		btnWEBSetRule("web_sd_uhd_capacity", []string{"sd", "480i", "480p", "576i", "576p", "2160p"}, 1),
	}
}

func btnSeasonPackSetRule(id string, origin string, capacity int) trackers.DupeSetRule {
	return trackers.DupeSetRule{
		ID:         "standalone/btn/duplicate/v1/" + id,
		EvidenceID: btnDupeEvidenceID,
		TargetPredicates: []trackers.DupeSetPredicate{
			btnSetPredicate(trackers.DupeDimensionPack, "true"),
			btnSetPredicate(trackers.DupeDimensionReleaseOrigin, origin),
		},
		CandidatePredicates: []trackers.DupeSetPredicate{
			btnSetPredicate(trackers.DupeDimensionPack, "true"),
			btnSetPredicate(trackers.DupeDimensionReleaseOrigin, origin),
			btnMatchingSetPredicate(trackers.DupeDimensionSeason),
			btnMatchingSetPredicate(trackers.DupeDimensionSource),
			btnMatchingSetPredicate(trackers.DupeDimensionResolution),
		},
		Capacity: capacity,
	}
}

func btnWEBSetRule(id string, resolutions []string, capacity int) trackers.DupeSetRule {
	return trackers.DupeSetRule{
		ID:         "standalone/btn/duplicate/v1/" + id,
		EvidenceID: btnDupeEvidenceID,
		TargetPredicates: []trackers.DupeSetPredicate{
			btnSetPredicate(trackers.DupeDimensionPack, "true"),
			btnSetPredicate(trackers.DupeDimensionSource, "web"),
			btnSetPredicate(trackers.DupeDimensionResolution, resolutions...),
			btnSetPredicate(trackers.DupeDimensionProvider),
		},
		CandidatePredicates: []trackers.DupeSetPredicate{
			btnSetPredicate(trackers.DupeDimensionPack, "true"),
			btnSetPredicate(trackers.DupeDimensionSource, "web"),
			btnMatchingSetPredicate(trackers.DupeDimensionSeason),
			btnMatchingSetPredicate(trackers.DupeDimensionResolution),
			btnMatchingSetPredicate(trackers.DupeDimensionProvider),
		},
		Capacity: capacity,
	}
}

func btnSetPredicate(dimension trackers.DupeDimension, values ...string) trackers.DupeSetPredicate {
	return trackers.DupeSetPredicate{
		Dimension:        dimension,
		Values:           values,
		RequiresComplete: true,
	}
}

func btnMatchingSetPredicate(dimension trackers.DupeDimension) trackers.DupeSetPredicate {
	return trackers.DupeSetPredicate{
		Dimension:        dimension,
		RequiresComplete: true,
		MatchTarget:      true,
	}
}
