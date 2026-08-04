// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const ptpDupeEvidenceID = "ptp-upload-rules-coexisting-trumping"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:         "ptp/duplicate/v2",
		EvidenceID: ptpDupeEvidenceID,
		SearchScope: trackers.DupeSearchScope{
			MaxPages: 1,
		},
		SlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionMediaKind,
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionHDR,
		},
		OptionalSlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionEdition,
		},
		CompleteSlotDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionEdition,
		},
		SuppressGeneralCoexistence: []trackers.DupeDimension{
			trackers.DupeDimensionEdition,
			trackers.DupeDimensionRegion,
		},
		HDRPartialMode:            trackers.DupeHDRPartialExplicitTitle,
		RequireDolbyVisionProfile: true,
		DolbyVisionProfile5Slot:   true,
		TrumpableOverridesSlot:    true,
		CoexistenceRules: []trackers.DupeRule{
			ptpPALNTSCFullDVDRule(),
			ptp1080pHDRX265Rule(true),
			ptp1080pHDRX265Rule(false),
		},
		PrecedenceRules: []trackers.DupeRule{
			ptpStructuredExactRule(),
			ptpWEBResolutionRule(true),
			ptpWEBResolutionRule(false),
		},
		SetRules: ptpEncodeSetRules(),
	}
}

func ptpStructuredExactRule() trackers.DupeRule {
	conditions := make([]trackers.DupeCondition, 0, 5)
	for _, dimension := range []trackers.DupeDimension{
		trackers.DupeDimensionContainer,
		trackers.DupeDimensionCodec,
		trackers.DupeDimensionResolution,
		trackers.DupeDimensionSize,
		trackers.DupeDimensionGroup,
	} {
		conditions = append(conditions, trackers.DupeCondition{
			Dimension:            dimension,
			ValuesEqual:          true,
			RequiresComplete:     true,
			MissingNotApplicable: true,
		})
	}
	return trackers.DupeRule{
		ID:         "ptp/duplicate/v2/structured_exact_identity",
		EvidenceID: ptpDupeEvidenceID,
		Conditions: conditions,
		Relation:   string(api.DupeRelationExactDuplicate),
		ReasonCode: "exact_identity",
	}
}

func ptpWEBResolutionRule(proposedHD bool) trackers.DupeRule {
	hd := []string{"720p", "1080p", "1080i", "1440p", "2160p"}
	sd := []string{"sd", "480p", "480i", "576p", "576i"}
	targetResolutions, candidateResolutions := hd, sd
	relation := api.DupeRelationProposedTrumps
	reason := "ptp_proposed_hd_webdl_over_sd"
	id := "ptp/duplicate/v2/proposed_hd_webdl_over_sd"
	if !proposedHD {
		targetResolutions, candidateResolutions = sd, hd
		relation = api.DupeRelationExistingPreferred
		reason = "ptp_existing_hd_webdl_over_sd"
		id = "ptp/duplicate/v2/existing_hd_webdl_over_sd"
	}
	return trackers.DupeRule{
		ID:               id,
		EvidenceID:       ptpDupeEvidenceID,
		Relation:         string(relation),
		ReasonCode:       reason,
		OverridesGeneral: true,
		Conditions: []trackers.DupeCondition{
			{
				Dimension:       trackers.DupeDimensionMediaKind,
				TargetValues:    []string{"web_dl"},
				CandidateValues: []string{"web_dl"},
			},
			{
				Dimension:       trackers.DupeDimensionResolution,
				TargetValues:    targetResolutions,
				CandidateValues: candidateResolutions,
			},
		},
	}
}

func ptpPALNTSCFullDVDRule() trackers.DupeRule {
	return trackers.DupeRule{
		ID:         "ptp/duplicate/v2/pal_ntsc_full_dvd",
		EvidenceID: ptpDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: "ptp_pal_ntsc_full_dvd",
		Conditions: []trackers.DupeCondition{
			{
				Dimension:       trackers.DupeDimensionMediaKind,
				TargetValues:    []string{"full_disc"},
				CandidateValues: []string{"full_disc"},
			},
			{
				Dimension:       trackers.DupeDimensionSource,
				TargetValues:    []string{"dvd"},
				CandidateValues: []string{"dvd"},
			},
			{
				Dimension:       trackers.DupeDimensionRegion,
				TargetValues:    []string{"pal", "ntsc"},
				CandidateValues: []string{"pal", "ntsc"},
				ValuesDifferent: true,
			},
		},
	}
}

func ptp1080pHDRX265Rule(proposedHDR bool) trackers.DupeRule {
	targetCodec, candidateCodec := []string{"h265"}, []string{"h264"}
	targetHDR, candidateHDR := []string{"hdr10", "hdr10_plus", "dolby_vision", "dolby_vision+hdr10"}, []string{"sdr"}
	id := "ptp/duplicate/v2/proposed_1080p_hdr_x265_slot"
	reason := "ptp_distinct_1080p_hdr_x265_slot"
	if !proposedHDR {
		targetCodec, candidateCodec = candidateCodec, targetCodec
		targetHDR, candidateHDR = candidateHDR, targetHDR
		id = "ptp/duplicate/v2/existing_1080p_hdr_x265_slot"
	}
	return trackers.DupeRule{
		ID:         id,
		EvidenceID: ptpDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: reason,
		Conditions: []trackers.DupeCondition{
			{
				Dimension:       trackers.DupeDimensionResolution,
				TargetValues:    []string{"1080p"},
				CandidateValues: []string{"1080p"},
			},
			{
				Dimension:       trackers.DupeDimensionCodec,
				TargetValues:    targetCodec,
				CandidateValues: candidateCodec,
			},
			{
				Dimension:       trackers.DupeDimensionHDR,
				TargetValues:    targetHDR,
				CandidateValues: candidateHDR,
			},
		},
	}
}

func ptpEncodeSetRules() []trackers.DupeSetRule {
	sdr := ptpSetPredicate(trackers.DupeDimensionHDR, "sdr")
	sd := ptpEncodeSetRule(
		"ptp/duplicate/v2/sd_x264_capacity",
		2,
		40,
		ptpSetPredicate(trackers.DupeDimensionResolution, "sd", "480p", "480i", "540p", "576i"),
		ptpSetPredicate(trackers.DupeDimensionCodec, "h264"),
		sdr,
	)
	sd.CapacityOverrides = []trackers.DupeSetCapacityOverride{{
		Capacity: 1,
		CandidatePredicates: []trackers.DupeSetPredicate{
			ptpSetPredicate(trackers.DupeDimensionResolution, "576p", "720p", "1080p", "1080i", "2160p", "4320p", "8640p"),
			{
				Dimension:        trackers.DupeDimensionEdition,
				RequiresComplete: true,
				MatchTarget:      true,
				Optional:         true,
			},
		},
	}}
	return []trackers.DupeSetRule{
		sd,
		ptpEncodeSetRule(
			"ptp/duplicate/v2/720p_x264_capacity",
			2,
			20,
			ptpSetPredicate(trackers.DupeDimensionResolution, "720p"),
			ptpSetPredicate(trackers.DupeDimensionCodec, "h264"),
			sdr,
		),
		ptpEncodeSetRule(
			"ptp/duplicate/v2/1080p_x264_capacity",
			2,
			20,
			ptpSetPredicate(trackers.DupeDimensionResolution, "1080p"),
			ptpSetPredicate(trackers.DupeDimensionCodec, "h264"),
			sdr,
		),
		ptpEncodeSetRule(
			"ptp/duplicate/v2/2160p_x265_hdr_capacity",
			2,
			20,
			ptpSetPredicate(trackers.DupeDimensionResolution, "2160p"),
			ptpSetPredicate(trackers.DupeDimensionCodec, "h265"),
			trackers.DupeSetPredicate{
				Dimension:        trackers.DupeDimensionHDR,
				ExcludedValues:   []string{"sdr", "dolby_vision"},
				RequiresComplete: true,
			},
		),
		ptpEncodeSetRule(
			"ptp/duplicate/v2/2160p_sdr_capacity",
			2,
			20,
			ptpSetPredicate(trackers.DupeDimensionResolution, "2160p"),
			sdr,
		),
		ptpEncodeSetRule(
			"ptp/duplicate/v2/576p_hd_source_x264_capacity",
			1,
			0,
			ptpSetPredicate(trackers.DupeDimensionResolution, "576p"),
			ptpSetPredicate(trackers.DupeDimensionCodec, "h264"),
			ptpSetPredicate(trackers.DupeDimensionSource, "bluray", "uhd_bluray", "hd_dvd", "hdtv", "web"),
			sdr,
		),
	}
}

func ptpEncodeSetRule(
	id string,
	capacity int,
	minimumSizeSeparation float64,
	familyPredicates ...trackers.DupeSetPredicate,
) trackers.DupeSetRule {
	encodeKinds := []string{"disc_encode", "dvd_rip", "other_encode", "web_rip"}
	targetPredicates := append([]trackers.DupeSetPredicate{
		ptpSetPredicate(trackers.DupeDimensionMediaKind, encodeKinds...),
	}, familyPredicates...)
	candidatePredicates := append([]trackers.DupeSetPredicate{
		{
			Dimension:        trackers.DupeDimensionMediaKind,
			Values:           encodeKinds,
			RequiresComplete: true,
			MatchTarget:      true,
		},
	}, familyPredicates...)
	candidatePredicates = append(candidatePredicates, trackers.DupeSetPredicate{
		Dimension:        trackers.DupeDimensionEdition,
		RequiresComplete: true,
		MatchTarget:      true,
		Optional:         true,
	})
	return trackers.DupeSetRule{
		ID:                           id,
		EvidenceID:                   ptpDupeEvidenceID,
		TargetPredicates:             targetPredicates,
		CandidatePredicates:          candidatePredicates,
		Capacity:                     capacity,
		MinimumSizeSeparationPercent: minimumSizeSeparation,
	}
}

func ptpSetPredicate(dimension trackers.DupeDimension, values ...string) trackers.DupeSetPredicate {
	return trackers.DupeSetPredicate{
		Dimension:        dimension,
		Values:           values,
		RequiresComplete: true,
	}
}
