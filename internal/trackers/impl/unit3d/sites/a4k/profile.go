package a4k

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns A4K's restricted type and resolution mappings together with
// its tracker-owned rules, banned groups, and image-host policy.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "A4K",
		BaseURL:      "https://aura4k.net",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
		ImageHost: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"onlyimage", "imgbox", "ptscreens", "imgbb", "imgur", "postimg"},
		},
	}
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"ENCODE": "3",
	}[unit3d.InferType(meta)]
}

func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{"4320p": "1", "2160p": "2"}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
