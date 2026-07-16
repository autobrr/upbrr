// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns SHRI's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "SHRI",
		BaseURL: "https://shareisland.org",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ResolveTypeID:          typeID,
			ApplyAdditionalPayload: additionalPayload,
			FinalizeDescription:    finalizeDescription,
		},
	}
}

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "26",
		"REMUX":  "7",
		"WEBDL":  "27",
		"WEBRIP": "15",
		"HDTV":   "33",
		"ENCODE": "15",
		"DVDRIP": "15",
	}[unit3d.InferType(meta)]
}

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	if value := numericValue(req.Meta.Region); value != "" {
		data["region_id"] = value
	}
	if value := numericValue(req.Meta.Distributor); value != "" {
		data["distributor_id"] = value
	}
}

func finalizeDescription(description string, meta api.UploadSubject) string {
	if !strings.EqualFold(releaseGroup(meta), "island") {
		return description
	}
	const notes = "Release Shareisland 🏴‍☠️\nFalla girare, condividila e contribuisci a mantenerla viva restando in seed il più possi" + "bile.\nGrazie per il supporto!"
	trimmed := strings.TrimSpace(description)
	if strings.Contains(trimmed, notes) {
		return trimmed
	}
	if trimmed == "" {
		return notes
	}
	return trimmed + "\n\n" + notes
}

func releaseGroup(meta api.UploadSubject) string {
	for _, value := range []string{meta.Release.Group, meta.ArrReleaseGroup, strings.TrimPrefix(meta.Tag, "-")} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func numericValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return trimmed
}
