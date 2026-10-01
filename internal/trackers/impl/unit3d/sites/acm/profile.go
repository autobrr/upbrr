// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ACM's Unit3D site manifest.
func Profile() unit3d.Profile {
	return profileWithTaxonomy(unit3d.SiteProfile{
		BuildDescription:    buildACMDescription,
		AdjustSearchParams:  adjustSearchParams,
		ResolveKeywords:     resolveACMKeywords,
		ResolveTypeID:       resolveUnit3DACMTypeID,
		ResolveResolutionID: resolveUnit3DACMResolutionID,
	})
}

func profileWithTaxonomy(site unit3d.SiteProfile) unit3d.Profile {
	regionID, distributorID := site.RegionID, site.DistributorID
	site.ApplyAdditionalPayload = func(req trackers.PreparationInput, data map[string]string) {
		additionalPayload(req, data, regionID, distributorID)
	}
	return unit3d.Profile{
		Name:             "ACM",
		BaseURL:          "https://eiga.moi",
		DescriptionGroup: "acm",
		ValidationPolicy: validationPolicy(regionID, distributorID),
		UploadArtifact: &trackers.UploadArtifactPolicy{
			Source: "AsianCinema",
		},
		ReleaseNamePolicy: namePolicy(),
		Site:              site,
	}
}
