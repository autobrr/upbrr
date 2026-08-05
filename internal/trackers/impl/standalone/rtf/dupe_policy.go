// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const rtfDupeEvidenceID = "rtf-upload-rules"

func duplicatePolicy() *trackers.DupePolicy {
	return &trackers.DupePolicy{
		ID:         "rtf/duplicate/v2",
		EvidenceID: rtfDupeEvidenceID,
		SearchScope: trackers.DupeSearchScope{
			MaxPages: 1,
		},
		RequiredDimensions: []trackers.DupeDimension{
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionMediaKind,
		},
		SuppressGeneralCoexistence: []trackers.DupeDimension{
			trackers.DupeDimensionResolution,
			trackers.DupeDimensionMediaClass,
			trackers.DupeDimensionEdition,
			trackers.DupeDimensionRegion,
			trackers.DupeDimensionThreeD,
		},
		CoexistenceRules: []trackers.DupeRule{
			rtfPALNTSCFullDVDRule(),
		},
		PrecedenceRules: append(rtfResolutionPrecedenceRules(), rtfRemuxPrecedenceRules()...),
		ManualReviewRules: []trackers.DupeRule{
			{
				ID:                 "rtf/duplicate/v2/container_direction_manual",
				EvidenceID:         rtfDupeEvidenceID,
				ReasonCode:         "rtf_container_direction_manual",
				RequiresManualStep: true,
				Conditions: []trackers.DupeCondition{{
					Dimension:            trackers.DupeDimensionContainer,
					ValuesDifferent:      true,
					MissingNotApplicable: true,
				}},
			},
		},
	}
}

func rtfResolutionPrecedenceRules() []trackers.DupeRule {
	classes := [][]string{
		{"sd", "480p", "480i"},
		{"720p"},
		{"1080p", "1080i"},
		{"2160p"},
	}
	rules := make([]trackers.DupeRule, 0, 12)
	for higher := 1; higher < len(classes); higher++ {
		for lower := 0; lower < higher; lower++ {
			rules = append(rules,
				rtfResolutionRule(
					"rtf/duplicate/v2/proposed_resolution_upgrade_"+classes[higher][0]+"_over_"+classes[lower][0],
					classes[higher],
					classes[lower],
					api.DupeRelationProposedTrumps,
					"rtf_proposed_resolution_upgrade",
				),
				rtfResolutionRule(
					"rtf/duplicate/v2/existing_resolution_upgrade_"+classes[higher][0]+"_over_"+classes[lower][0],
					classes[lower],
					classes[higher],
					api.DupeRelationExistingPreferred,
					"rtf_existing_resolution_upgrade",
				),
			)
		}
	}
	return rules
}

func rtfResolutionRule(
	id string,
	targetResolutions []string,
	candidateResolutions []string,
	relation api.DupeRelation,
	reason string,
) trackers.DupeRule {
	return trackers.DupeRule{
		ID:               id,
		EvidenceID:       rtfDupeEvidenceID,
		Relation:         string(relation),
		ReasonCode:       reason,
		OverridesGeneral: true,
		Conditions: []trackers.DupeCondition{
			{
				Dimension:       trackers.DupeDimensionResolution,
				TargetValues:    targetResolutions,
				CandidateValues: candidateResolutions,
			},
			{
				Dimension:        trackers.DupeDimensionMediaKind,
				RequiresComplete: true,
			},
		},
	}
}

func rtfRemuxPrecedenceRules() []trackers.DupeRule {
	nonRemux := []string{"disc_encode", "web_dl", "web_rip", "hdtv", "sdtv", "dvd_rip", "other_encode"}
	return []trackers.DupeRule{
		{
			ID:               "rtf/duplicate/v2/proposed_remux_upgrade",
			EvidenceID:       rtfDupeEvidenceID,
			Relation:         string(api.DupeRelationProposedTrumps),
			ReasonCode:       "rtf_proposed_remux_upgrade",
			OverridesGeneral: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    []string{"remux"},
					CandidateValues: nonRemux,
				},
				{Dimension: trackers.DupeDimensionResolution, RequiresComplete: true},
			},
		},
		{
			ID:               "rtf/duplicate/v2/existing_remux_upgrade",
			EvidenceID:       rtfDupeEvidenceID,
			Relation:         string(api.DupeRelationExistingPreferred),
			ReasonCode:       "rtf_existing_remux_upgrade",
			OverridesGeneral: true,
			Conditions: []trackers.DupeCondition{
				{
					Dimension:       trackers.DupeDimensionMediaKind,
					TargetValues:    nonRemux,
					CandidateValues: []string{"remux"},
				},
				{Dimension: trackers.DupeDimensionResolution, RequiresComplete: true},
			},
		},
	}
}

func rtfPALNTSCFullDVDRule() trackers.DupeRule {
	return trackers.DupeRule{
		ID:         "rtf/duplicate/v2/pal_ntsc_full_dvd",
		EvidenceID: rtfDupeEvidenceID,
		Relation:   string(api.DupeRelationCoexists),
		ReasonCode: "rtf_pal_ntsc_full_dvd",
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
