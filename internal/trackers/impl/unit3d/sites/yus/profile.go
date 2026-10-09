package yus

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns YUS's type mapping, naming-evidence policy and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		NameProviders:     &unit3d.NameProviders{Title: api.IdentityProviderIMDB, MovieYear: api.IdentityProviderTMDB},
		Name:              "YUS",
		BaseURL:           "https://yu-scene.net",
		BannedGroups:      BannedGroups(),
		ReleaseNamePolicy: namePolicy(),
		ValidationPolicy:  validationPolicy(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
