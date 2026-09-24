package oe

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns OE's no-group naming, codec-specific encode types, and
// tracker-owned dupe, image-host, rule, and banned-group policies.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:              "OE",
		BaseURL:           "https://onlyencodes.cc",
		Rules:             Rules(),
		ValidationPolicy:  ValidationPolicy(),
		BannedGroups:      BannedGroups(),
		ReleaseNamePolicy: namePolicy(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:          typeID,
			ApplyAdditionalPayload: additionalPayload,
		},
		ImageHost: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"imgbox", "imgbb", "onlyimage", "ptscreens", "passtheimage"},
		},
	}
}
