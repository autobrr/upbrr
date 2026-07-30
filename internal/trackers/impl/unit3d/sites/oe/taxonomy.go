package oe

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

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
