package znth

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns ZNTH's tracker-specific name policy and type mapping,
// including its dedicated DVDRip type ID.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "ZNTH",
		BaseURL: "https://znth.cx",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			BuildName:     buildName,
			ResolveTypeID: typeID,
		},
	}
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"ENCODE": "3",
		"DVDRIP": "11",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
	}[unit3d.InferType(meta)]
}
