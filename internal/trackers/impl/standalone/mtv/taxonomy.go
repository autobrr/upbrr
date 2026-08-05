// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveResolution(meta api.UploadSubject) string {
	if value := strings.TrimSpace(meta.Release.Resolution); value != "" {
		return value
	}
	if strings.TrimSpace(meta.UHD) != "" {
		return "2160p"
	}
	return ""
}

func resolveResolutionID(meta api.UploadSubject) string {
	res := strings.ToLower(strings.TrimSpace(resolveResolution(meta)))
	values := map[string]string{
		"8640p": "0",
		"4320p": "4000",
		"2160p": "2160",
		"1440p": "1440",
		"1080p": "1080",
		"1080i": "1080",
		"720p":  "720",
		"576p":  "0",
		"576i":  "0",
		"480p":  "480",
		"480i":  "480",
	}
	if value, ok := values[res]; ok {
		return value
	}
	return "10"
}

func isSD(meta api.UploadSubject) bool {
	res := strings.ToLower(resolveResolution(meta))
	return strings.Contains(res, "480") || strings.Contains(res, "576")
}

func resolveCategory(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
}

func resolveCategoryID(meta api.UploadSubject) string {
	category := resolveCategory(meta)
	if category == "MOVIE" {
		if isSD(meta) {
			return "2"
		}
		return "1"
	}
	if category == "TV" {
		if meta.TVPack {
			if isSD(meta) {
				return "6"
			}
			return "5"
		}
		if isSD(meta) {
			return "4"
		}
		return "3"
	}
	return "0"
}

func resolveType(meta api.UploadSubject) string {
	value := strings.ToUpper(strings.TrimSpace(meta.Type))
	if value == "" {
		value = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	return strings.ReplaceAll(strings.ReplaceAll(value, "-", ""), " ", "")
}

func resolveSourceID(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "1"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || resolveType(meta) == "REMUX" {
		return "7"
	}
	mapping := map[string]string{
		"DISC":   "1",
		"WEBDL":  "9",
		"WEBRIP": "10",
		"HDTV":   "1",
		"SDTV":   "2",
		"TVRIP":  "3",
		"DVD":    "4",
		"DVDRIP": "5",
		"BDRIP":  "8",
		"VHS":    "6",
		"MIXED":  "11",
		"ENCODE": "7",
	}
	if value, ok := mapping[resolveType(meta)]; ok {
		return value
	}
	return "0"
}

func resolveOriginID(meta api.UploadSubject) string {
	if meta.PersonalRelease {
		return "4"
	}
	if meta.Scene {
		return "2"
	}
	return "3"
}
