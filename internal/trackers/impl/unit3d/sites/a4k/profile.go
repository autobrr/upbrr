package a4k

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns A4K's restricted type and resolution mappings together with
// its tracker-owned rules, banned groups, and image-host policy.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "A4K",
		BaseURL:      "https://aura4k.net",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
		ImageHost: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"onlyimage", "imgbox", "ptscreens", "imgbb", "imgur", "postimg"},
		},
	}
}
