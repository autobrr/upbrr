package rf

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "43",
		"REMUX":  "40",
		"WEBDL":  "42",
		"WEBRIP": "45",
		"ENCODE": "41",
		"HDTV":   "35",
	}[unit3d.InferType(meta)]
}
func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "6",
		"576i":  "7",
		"480p":  "8",
		"480i":  "9",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
