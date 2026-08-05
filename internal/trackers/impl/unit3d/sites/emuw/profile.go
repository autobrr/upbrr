package emuw

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns EMUW's type and resolution mappings. Unknown types use the
// encode ID, while unknown resolutions use the site's Other ID.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "EMUW",
		BaseURL: "https://emuwarez.com",
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}
