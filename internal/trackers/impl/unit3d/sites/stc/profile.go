package stc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "STC",
		BaseURL: "https://skipthecommercials.xyz",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
		ImageHost: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"imgbox", "imgbb"},
		},
	}
}

func typeID(meta api.UploadSubject) string {
	typeValue := unit3d.InferType(meta)
	if meta.TVPack {
		isWeb := typeValue == "WEBDL" || typeValue == "WEBRIP"
		sd := unit3d.IsSDResolution(unit3d.Resolution(meta))
		if sd && isWeb {
			return "14"
		}
		if sd {
			return "17"
		}
		if isWeb {
			return "13"
		}
		return "18"
	}
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
		"ENCODE": "3",
	}[typeValue]
}
