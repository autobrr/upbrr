package tlz

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns TLZ's distinct type IDs for movies, packs, and remaining
// content.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "TLZ",
		BaseURL: "https://tlzdigital.com",
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
