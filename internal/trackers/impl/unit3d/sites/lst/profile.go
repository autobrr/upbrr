// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns LST's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "LST",
		BaseURL:          "https://lst.gg",
		Rules:            Rules(),
		ValidationPolicy: validationPolicy(),
		Site: unit3d.SiteProfile{
			ApplyAdditionalPayload: additionalPayload,
		},
		DupePolicy: &trackers.DupePolicy{
			TrackTrumpableID:     true,
			MatchDVDReleaseGroup: true,
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			EndpointPath:  "/api/bannedReleaseGroups",
			RequireAPIKey: true,
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "lostimg",
			OwnedHosts:             []string{"lostimg"},
			EnableWithImageHosting: true,
		},
	}
}
