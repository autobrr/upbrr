// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUnit3DACMTypeID(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		sizeBucket := acmDiscBucket(meta.SourceSize)
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && sizeBucket != 25 {
			switch sizeBucket {
			case 50:
				return "3"
			case 66:
				return "2"
			case 100:
				return "1"
			}
		}
		switch sizeBucket {
		case 25:
			return "5"
		case 50:
			return "4"
		}
		return "0"
	}

	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		switch acmDVDType(meta) {
		case "DVD 5":
			return "14"
		case "DVD 9":
			return "16"
		default:
			return "0"
		}
	}

	switch acmNonDiscType(meta) {
	case "UHD REMUX":
		return "12"
	case "REMUX":
		return "7"
	case "WEBDL":
		return "9"
	case "SDTV":
		return "13"
	case "HDTV":
		return "17"
	default:
		return "0"
	}
}

func resolveUnit3DACMResolutionID(meta api.UploadSubject) string {
	switch strings.ToLower(strings.TrimSpace(unit3d.Resolution(meta))) {
	case "2160p":
		return "1"
	case "1080p", "1080i":
		return "2"
	case "720p":
		return "3"
	case "576p", "576i":
		return "4"
	case "480p", "480i":
		return "5"
	default:
		return "10"
	}
}

func resolveACMKeywords(meta api.UploadSubject) string {
	raw := unit3d.Keywords(meta)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" || strings.Contains(trimmed, " ") {
			continue
		}
		filtered = append(filtered, trimmed)
		if len(filtered) >= 10 {
			break
		}
	}
	return strings.Join(filtered, ", ")
}

func acmDiscBucket(sourceSize int64) int {
	if sourceSize <= 0 {
		return 100
	}
	sizeGiB := float64(sourceSize) / float64(1<<30)
	for _, bucket := range []int{25, 50, 66, 100} {
		if sizeGiB < float64(bucket) {
			return bucket
		}
	}
	return 100
}

func acmDVDType(meta api.UploadSubject) string {
	name := strings.ToUpper(strings.TrimSpace(baseName(meta)))
	switch {
	case strings.Contains(name, "DVD5"):
		return "DVD 5"
	case strings.Contains(name, "DVD9"):
		return "DVD 9"
	case meta.SourceSize > 0 && meta.SourceSize <= 5*(1<<30):
		return "DVD 5"
	case meta.SourceSize > 0:
		return "DVD 9"
	default:
		return ""
	}
}

func acmNonDiscType(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(unit3d.InferType(meta)), "REMUX") && strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") {
		return "UHD REMUX"
	}
	switch strings.ToUpper(strings.TrimSpace(unit3d.InferType(meta))) {
	case "WEBDL", "WEB-DL":
		return "WEBDL"
	case "HDTV":
		if strings.HasPrefix(strings.TrimSpace(unit3d.Resolution(meta)), "4") || strings.HasPrefix(strings.TrimSpace(unit3d.Resolution(meta)), "5") {
			return "SDTV"
		}
		return "HDTV"
	case "REMUX":
		return "REMUX"
	default:
		return ""
	}
}
