package otw

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns OTW's disc and DVDRip type mapping together with duplicate
// resolution-mismatch rejection, rules, and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "OTW",
		BaseURL:          "https://oldtoons.world",
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
		DupePolicy: &trackers.DupePolicy{
			ID: "otw/duplicate/v1",
			SearchScope: trackers.DupeSearchScope{
				IncludeEpisodes:    true,
				IncludeSeasonPacks: true,
				MaxPages:           100,
			},
			RequiredEvidence: trackers.DupeEvidenceRequirements{HDR: true, Resolution: true},
			SlotDimensions:   []trackers.DupeDimension{trackers.DupeDimensionType, trackers.DupeDimensionResolution, trackers.DupeDimensionHDR},
		},
	}
}
