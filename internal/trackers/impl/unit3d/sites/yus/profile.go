package yus

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns YUS's site-specific type mapping and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "YUS",
		BaseURL:      "https://yu-scene.net",
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
