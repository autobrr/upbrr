package a4k

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	aiRegex      = regexp.MustCompile(`(?i)(^|[^[:alnum:]])ai([^[:alnum:]]|$)`)
	upscaleRegex = regexp.MustCompile(`(?i)(^|[^[:alnum:]])upscaled?([^[:alnum:]]|$)`)
	fanresRegex  = regexp.MustCompile(`(?i)(^|[^[:alnum:]])(?:fanres|35mm|no[ ._-]?dnr)([^[:alnum:]]|$)`)
)

func markerName(meta api.UploadSubject) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	return strings.TrimSpace(meta.ReleaseNameNoTag)
}

func typeID(meta api.UploadSubject) string {
	name := markerName(meta)
	if upscaleRegex.MatchString(name) || aiRegex.MatchString(name) {
		return "8"
	}
	if fanresRegex.MatchString(name) {
		return "7"
	}
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"ENCODE": "3",
	}[unit3d.InferType(meta)]
}

func resolutionID(meta api.UploadSubject) string {
	if strings.EqualFold(unit3d.Resolution(meta), "2160p") {
		return "2"
	}
	if unit3d.Resolution(meta) == "" {
		return "10"
	}
	return "10"
}
