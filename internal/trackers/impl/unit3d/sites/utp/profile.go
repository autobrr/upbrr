package utp

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns UTP's space-delimited release-name construction, image URL
// swap, tracker-owned rules and audio policy, optional owned image host, and
// type and resolution mappings, including fallback IDs for unknown values.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:        "UTP",
		BaseURL:     "https://utp.to",
		Rules:       Rules(),
		AudioPolicy: AudioPolicy(),
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			BuildNameVersion:    "v1",
			BuildDescription:    buildDescription,
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "utppm",
			OwnedHosts:             []string{"utppm"},
			EnableWithImageHosting: true,
		},
	}
}
