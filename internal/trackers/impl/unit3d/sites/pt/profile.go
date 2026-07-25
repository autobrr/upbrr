// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pt

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns PT's type and resolution mappings and adds European
// Portuguese audio and subtitle flags to the upload payload.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "PT",
		BaseURL: "https://portugas.org",
		Site: unit3d.SiteProfile{
			ResolveTypeID:          typeID,
			ResolveResolutionID:    resolutionID,
			ApplyAdditionalPayload: additionalPayload,
		},
	}
}
