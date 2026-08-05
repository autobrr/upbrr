// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) int {
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if category == "TV" {
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
			if resolution == "2160p" {
				return 72
			}
			return 59
		}
		if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
				return 73
			}
			return 60
		}
		switch resolution {
		case "2160p":
			return 65
		case "1080p", "1080i":
			return 30
		default:
			return 38
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		if resolution == "2160p" {
			return 70
		}
		return 1
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
			return 71
		}
		return 2
	}
	switch resolution {
	case "2160p":
		return 64
	case "1080p", "1080i":
		return 5
	default:
		return 3
	}
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return string(category)
}
