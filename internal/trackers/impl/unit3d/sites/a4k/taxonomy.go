package a4k

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

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
