package a4k

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func typeID(meta api.UploadSubject) string {
	if a4kHasOther(meta, "AI.Upscale", "upscaled (ai)", "upscaled", "AI Remaster") {
		return "8"
	}
	if a4kHasSource(meta, "35mm") || a4kHasOther(meta, "FANRES", "no-DNR", "No Digital Noise Reduction") {
		return "7"
	}
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"ENCODE": "3",
	}[unit3d.InferType(meta)]
}

func a4kHasSource(meta api.UploadSubject, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(meta.Source), candidate) {
			return true
		}
	}
	return false
}

func a4kHasOther(meta api.UploadSubject, candidates ...string) bool {
	for _, value := range meta.Release.Other {
		for _, candidate := range candidates {
			if strings.EqualFold(strings.TrimSpace(value), candidate) {
				return true
			}
		}
	}
	return false
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
