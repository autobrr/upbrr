package r4e

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "R4E",
		BaseURL: "https://racing4everyone.eu",
		Site: unit3d.SiteProfile{
			ResolveCategoryID: categoryID,
		},
	}
}
func categoryID(meta api.UploadSubject) string {
	genreIDs := ""
	if meta.ProviderMetadata.TMDB != nil {
		genreIDs = meta.ProviderMetadata.TMDB.GenreIDs
	}
	isDoc := false
	for value := range strings.SplitSeq(genreIDs, ",") {
		if strings.TrimSpace(value) == "99" {
			isDoc = true
			break
		}
	}
	switch {
	case strings.EqualFold(unit3d.Category(meta), "MOVIE") && isDoc:
		return "66"
	case strings.EqualFold(unit3d.Category(meta), "MOVIE"):
		return "70"
	case strings.EqualFold(unit3d.Category(meta), "TV") && isDoc:
		return "2"
	case strings.EqualFold(unit3d.Category(meta), "TV"):
		return "79"
	default:
		return "24"
	}
}
