package rf

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns RF's no-group naming, site-specific type and resolution
// mappings, required release-group dupe policy, and optional owned image host.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "RF",
		BaseURL: "https://reelflix.cc",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			BuildName:           buildName,
			BuildNameVersion:    "v1",
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
		DupePolicy: &trackers.DupePolicy{
			RequireReleaseGroup: true,
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:        "reelflix",
			OwnedHosts:             []string{"reelflix"},
			EnableWithImageHosting: true,
		},
		TorrentIdentity: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://reelflix.xyz"},
			CommentURLPatterns: []string{"https://reelflix.xyz"},
			DetailIDPattern:    `(?i)reelflix\.(?:cc|xyz)/torrents/(\d+)`,
		},
	}
}
