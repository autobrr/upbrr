// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns SAM's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:              "SAM",
		BaseURL:           "https://samaritano.cc",
		ReleaseNamePolicy: namePolicy(),
		ValidationPolicy:  validationPolicy(),
		DupePolicy:        duplicatePolicy(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			InspectProgrammeTracks: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "samaritano",
			OwnedHosts:             []string{"samaritano"},
			EnableWithImageHosting: true,
		},
		Site: unit3d.SiteProfile{
			CategoryIDs:       categoryIDs,
			ResolveCategoryID: categoryID,
		},
	}
}
