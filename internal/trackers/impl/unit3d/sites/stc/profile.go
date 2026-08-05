package stc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns STC's type mapping, including dedicated TV-pack IDs split by
// web source and SD status, plus its image-host and rule policies.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "STC",
		BaseURL: "https://skipthecommercials.xyz",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
		ImageHost: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"imgbox", "imgbb"},
		},
	}
}
