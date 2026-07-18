package itt

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns ITT's type mapping, preferring recognized release-name type
// markers before falling back to the inferred Unit3D type.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "ITT",
		BaseURL: "https://itatorrents.xyz",
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}

func typeID(meta api.UploadSubject) string {
	name := strings.ToUpper(strings.TrimSpace(meta.ReleaseName))
	for marker, id := range map[string]string{
		"DLMUX":  "27",
		"BDMUX":  "29",
		"WEBMUX": "26",
		"DVDMUX": "39",
		"BDRIP":  "25",
	} {
		if strings.Contains(name, marker) {
			return id
		}
	}
	return map[string]string{
		"DISC":      "1",
		"REMUX":     "2",
		"WEBDL":     "4",
		"WEBRIP":    "5",
		"HDTV":      "6",
		"ENCODE":    "3",
		"DVDRIP":    "24",
		"CINEMA-MD": "14",
	}[unit3d.InferType(meta)]
}
