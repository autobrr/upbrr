package emuw

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "EMUW",
		BaseURL: "https://emuwarez.com",
		Site: unit3d.SiteProfile{
			ResolveTypeID:       typeID,
			ResolveResolutionID: resolutionID,
		},
	}
}

func typeID(meta api.UploadSubject) string {
	value := map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"ENCODE": "3",
		"WEBDL":  "4",
		"WEBRIP": "5",
		"HDTV":   "6",
	}[unit3d.InferType(meta)]
	if value == "" {
		return "3"
	}
	return value
}

func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "6",
		"540p":  "7",
		"480p":  "8",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
