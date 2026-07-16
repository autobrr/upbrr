package yus

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "YUS",
		BaseURL:      "https://yu-scene.net",
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}
func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "17",
		"REMUX":  "2",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
		"ENCODE": "3",
	}[unit3d.InferType(meta)]
}
