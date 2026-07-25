package otw

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func typeID(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "1"
	}
	if unit3d.IsDiscType(meta.DiscType) {
		return "7"
	}
	typeValue := unit3d.InferType(meta)
	if typeValue == "DVDRIP" {
		return "8"
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
