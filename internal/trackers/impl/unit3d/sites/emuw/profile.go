package emuw

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns EMUW's type and resolution mappings. Unknown types use the
// encode ID, while unknown resolutions use the site's Other ID. Legacy torrent
// URLs remain recognized without sending API requests to the old domain.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:              "EMUW",
		BaseURL:           "https://emu-warez.com",
		ReleaseNamePolicy: namePolicy(),
		TorrentIdentity: &trackers.TorrentIdentityPolicy{
			TrackerURLPatterns: []string{"https://emuwarez.com"},
			CommentURLPatterns: []string{"https://emuwarez.com"},
		},
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}
