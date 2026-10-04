// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns SHRI's type mapping, standard region/distributor payload
// fields, and idempotent Island-group description footer.
func Profile() unit3d.Profile {
	return profileWithTaxonomy(unit3d.SiteProfile{
		ResolveTypeID:       typeID,
		FinalizeDescription: finalizeDescription,
	})
}

func profileWithTaxonomy(site unit3d.SiteProfile) unit3d.Profile {
	regionID, distributorID := site.RegionID, site.DistributorID
	site.ApplyAdditionalPayload = func(req trackers.PreparationInput, data map[string]string) {
		additionalPayload(req, data, regionID, distributorID)
	}
	return unit3d.Profile{
		Name:             "SHRI",
		BaseURL:          "https://shareisland.org",
		ValidationPolicy: validationPolicy(regionID, distributorID),
		Site:             site,
	}
}
