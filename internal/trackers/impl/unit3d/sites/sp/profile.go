// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns SP's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "SP",
		BaseURL:          "https://seedpool.org",
		Rules:            Rules(),
		ValidationPolicy: ValidationPolicy(),
		ReleaseNamePolicy: trackers.WithNonSceneReleaseNameConfirmation(
			trackers.NewReleaseNamePolicy("unit3d/sp/v3", func(input trackers.ReleaseNameInput) (trackers.ResolvedReleaseNames, error) {
				name := buildName(input.Subject, input.TrackerConfig)
				if !input.Subject.Scene && input.RequestedName != nil {
					name = strings.TrimSpace(*input.RequestedName)
				}
				return trackers.ResolvedReleaseNames{Upload: name}, nil
			}),
		),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v3",
		},
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDBIDOnly},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		DupePolicy: &trackers.DupePolicy{
			ID:         "sp/duplicate/v2",
			EvidenceID: "sp-upload-organization-guides",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions:  []trackers.DupeDimension{trackers.DupeDimensionType, trackers.DupeDimensionResolution, trackers.DupeDimensionHDR},
			PrecedenceRules: trackers.SeasonPackPrecedenceRules("sp-upload-organization-guides"),
		},
	}
}
