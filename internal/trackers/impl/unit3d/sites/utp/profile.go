package utp

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns UTP's type and resolution mappings, including fallback IDs
// for unknown values.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "UTP",
		BaseURL: "https://utp.to",
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}
