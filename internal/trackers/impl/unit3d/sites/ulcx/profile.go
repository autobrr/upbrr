// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ULCX's naming, duplicate, validation, and banned-group policy.
func Profile() unit3d.Profile {
	return profileWithTaxonomy(unit3d.SiteProfile{})
}

func profileWithTaxonomy(site unit3d.SiteProfile) unit3d.Profile {
	site.ProjectionQuestionnaire = languageQuestionnaire
	return unit3d.Profile{
		Name:              "ULCX",
		BaseURL:           "https://upload.cx",
		Rules:             Rules(),
		ValidationPolicy:  validationPolicy(site.RegionID),
		Site:              site,
		BannedGroups:      BannedGroups(),
		ReleaseNamePolicy: namePolicy(),
		DupePolicy:        duplicatePolicy(),
	}
}
