package yus

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns YUS's site-specific type mapping and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "YUS",
		BaseURL:      "https://yu-scene.net",
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v1",
			ResolveTypeID:    typeID,
		},
		DupePolicy: &trackers.DupePolicy{
			ID: "yus/duplicate/v1",
			SearchScope: trackers.DupeSearchScope{
				IncludeEpisodes:    true,
				IncludeSeasonPacks: true,
				MaxPages:           100,
			},
			RequiredEvidence: trackers.DupeEvidenceRequirements{HDR: true},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionSource,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionHDR,
			},
		},
	}
}
