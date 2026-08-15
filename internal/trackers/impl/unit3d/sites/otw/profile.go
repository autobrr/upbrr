package otw

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns OTW's disc and DVDRip type mapping together with duplicate
// resolution-mismatch rejection, rules, and banned groups.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:             "OTW",
		BaseURL:          "https://oldtoons.world",
		ValidationPolicy: ValidationPolicy(),
		BannedGroups:     BannedGroups(),
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			RequireKnownCategory: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope: trackers.MetadataScopeAny,
				AnyOf: []trackers.MetadataField{
					trackers.MetadataFieldTMDB,
					trackers.MetadataFieldIMDB,
					trackers.MetadataFieldTVDB,
				},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		Site: unit3d.SiteProfile{
			BuildName:        buildName,
			BuildNameVersion: "v3",
			ResolveTypeID:    typeID,
		},
		DupePolicy: &trackers.DupePolicy{
			ID:         "otw/duplicate/v2",
			EvidenceID: "otw-rules-naming",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions:  []trackers.DupeDimension{trackers.DupeDimensionType, trackers.DupeDimensionResolution, trackers.DupeDimensionHDR},
			PrecedenceRules: trackers.DirectionalMediaKindRules("otw-rules-naming", "web_dl", "web_rip"),
		},
	}
}
