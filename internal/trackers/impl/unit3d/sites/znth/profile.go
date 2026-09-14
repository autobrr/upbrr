package znth

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns ZNTH's tracker-specific name policy and type mapping,
// including its dedicated DVDRip type ID.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:              "ZNTH",
		BaseURL:           "https://znth.cx",
		Rules:             Rules(),
		ReleaseNamePolicy: namePolicy(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
