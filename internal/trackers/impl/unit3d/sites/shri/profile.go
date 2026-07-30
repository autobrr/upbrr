// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns SHRI's type mapping, numeric region/distributor payload
// fields, and idempotent Island-group description footer.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "SHRI",
		BaseURL:          "https://shareisland.org",
		ValidationPolicy: ValidationPolicy(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:          typeID,
			ApplyAdditionalPayload: additionalPayload,
			FinalizeDescription:    finalizeDescription,
		},
	}
}
