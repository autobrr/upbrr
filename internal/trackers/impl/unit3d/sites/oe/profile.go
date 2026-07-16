package oe

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "OE",
		BaseURL:      "https://onlyencodes.cc",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName:     buildName,
			ResolveTypeID: typeID,
		},
		DupePolicy: &trackers.DupePolicy{
			AllowSizeVariance1080: true,
		},
		ImageHost: &trackers.ImageHostPolicy{
			AllowedHosts: []string{"imgbox", "imgbb", "onlyimage", "ptscreens", "passtheimage"},
		},
	}
}
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))
	if tag != "" && !strings.EqualFold(tag, "nogrp") && !strings.EqualFold(tag, "nogroup") && !strings.EqualFold(tag, "unknown") &&
		!strings.EqualFold(tag, "-unk-") {
		return name
	}
	if name == "" || strings.HasSuffix(strings.ToUpper(name), "-NOGRP") {
		return name
	}
	return name + "-NOGRP"
}

func typeID(meta api.UploadSubject) string {
	typeValue := unit3d.InferType(meta)
	if typeValue == "DVDRIP" {
		typeValue = "ENCODE"
	}
	switch typeValue {
	case "DISC":
		return "19"
	case "REMUX":
		return "20"
	case "WEBDL":
		return "21"
	case "WEBRIP", "ENCODE":
		switch normalizeCodec(meta.VideoCodec) {
		case "HEVC":
			return "10"
		case "AV1":
			return "14"
		case "AVC":
			return "15"
		default:
			return "16"
		}
	default:
		return "16"
	}
}

func normalizeCodec(value string) string {
	codec := strings.ToUpper(strings.TrimSpace(value))
	switch {
	case strings.Contains(codec, "AV1"):
		return "AV1"
	case strings.Contains(codec, "HEVC") || strings.Contains(codec, "H.265") || strings.Contains(codec, "H265") || strings.Contains(codec, "X265"):
		return "HEVC"
	case strings.Contains(codec, "AVC") || strings.Contains(codec, "H.264") || strings.Contains(codec, "H264") || strings.Contains(codec, "X264"):
		return "AVC"
	default:
		return ""
	}
}
