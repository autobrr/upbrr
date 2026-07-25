package itt

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ITT's type mapping, preferring recognized release-name type
// markers before falling back to the inferred Unit3D type.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "ITT",
		BaseURL: "https://itatorrents.xyz",
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
