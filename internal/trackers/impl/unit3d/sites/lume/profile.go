// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns LUME's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "LUME",
		BaseURL:          "https://luminarr.me",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v2",
		},
		DupePolicy: &trackers.DupePolicy{
			ID:         "lume/duplicate/v2",
			EvidenceID: "lume-rules-naming",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionEdition,
				trackers.DupeDimensionProvider,
				trackers.DupeDimensionHDR,
			},
			HDRCompatibilityMode:      trackers.DupeHDRCompatibilityDirectional,
			RequireDolbyVisionProfile: true,
			PrecedenceRules:           lumeDupePrecedenceRules(),
			SizeVariancePercent:       20,
			SizeVarianceTypes:         []string{"ENCODE"},
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			TRaSHGuideURL: "https://raw.githubusercontent.com/TRaSH-Guides/Guides/refs/heads/master/docs/json/radarr/cf/lq.json",
		},
	}
}

func lumeDupePrecedenceRules() []trackers.DupeRule {
	rules := make([]trackers.DupeRule, 0, 10)
	for _, pair := range [][2]string{
		{"web_dl", "web_rip"},
		{"web_dl", "hdtv"},
		{"web_rip", "hdtv"},
		{"web_dl", "dvd_rip"},
		{"web_rip", "dvd_rip"},
	} {
		rules = append(rules, trackers.DirectionalMediaKindRules("lume-rules-naming", pair[0], pair[1])...)
	}
	return rules
}
