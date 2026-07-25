package r4e

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns R4E's category mapping, which separates movie and TV content
// and assigns documentary categories from TMDB genre ID 99.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "R4E",
		BaseURL: "https://racing4everyone.eu",
		Site: unit3d.SiteProfile{
			ResolveCategoryID: categoryID,
		},
	}
}
