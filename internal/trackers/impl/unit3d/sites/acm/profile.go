// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ACM's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "ACM",
		BaseURL:          "https://eiga.moi",
		DescriptionGroup: "acm",
		ValidationPolicy: validationPolicy(),
		UploadArtifact: &trackers.UploadArtifactPolicy{
			Source: "AsianCinema",
		},
		Site: unit3d.SiteProfile{
			BuildName:              buildName,
			BuildNameVersion:       "v2",
			BuildDescription:       buildACMDescription,
			ResolveKeywords:        resolveACMKeywords,
			ResolveTypeID:          resolveUnit3DACMTypeID,
			ResolveResolutionID:    resolveUnit3DACMResolutionID,
			ApplyAdditionalPayload: additionalPayload,
		},
	}
}
