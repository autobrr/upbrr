package tlz

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "TLZ",
		BaseURL: "https://tlzdigital.com",
		Site: unit3d.SiteProfile{
			ResolveTypeID: typeID,
		},
	}
}

func typeID(meta api.UploadSubject) string {
	if strings.EqualFold(unit3d.Category(meta), "MOVIE") {
		return "1"
	}
	if meta.TVPack {
		return "4"
	}
	return "3"
}
