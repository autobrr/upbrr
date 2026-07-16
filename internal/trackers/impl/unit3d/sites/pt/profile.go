// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package pt

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns PT's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "PT",
		BaseURL: "https://portugas.org",
		Site: unit3d.SiteProfile{
			ResolveTypeID:          typeID,
			ResolveResolutionID:    resolutionID,
			ApplyAdditionalPayload: additionalPayload,
		},
	}
}

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	data["audio_pt"] = boolString(hasEuropeanPortuguese(req.Meta.AudioLanguages))
	data["legenda_pt"] = boolString(hasEuropeanPortuguese(req.Meta.SubtitleLanguages))
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func hasEuropeanPortuguese(languages []string) bool {
	for _, language := range languages {
		lower := strings.ToLower(strings.TrimSpace(language))
		if lower == "" || strings.Contains(lower, "brazil") || strings.Contains(lower, "brasil") || strings.Contains(lower, "pt-br") ||
			strings.Contains(lower, "ptbr") {
			continue
		}
		if strings.Contains(lower, "portuguese") || lower == "pt" || strings.Contains(lower, "português") {
			return true
		}
	}
	return false
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "1",
		"REMUX":  "2",
		"WEBDL":  "4",
		"WEBRIP": "39",
		"HDTV":   "6",
		"ENCODE": "3",
	}[unit3d.InferType(meta)]
}

func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1440p": "13",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "6",
		"576i":  "7",
		"540p":  "11",
		"480p":  "8",
		"480i":  "9",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "10"
}
