// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func siteSHRIProfile() unit3DSiteProfile {
	return unit3DSiteProfile{
		resolveTypeID: resolveUnit3DSHRITypeID,
		applyAdditionalPayload: func(req trackers.UploadRequest, data map[string]string) {
			if regionID := numericValue(req.Meta.Region); regionID != "" {
				data["region_id"] = regionID
			}
			if distributorID := numericValue(req.Meta.Distributor); distributorID != "" {
				data["distributor_id"] = distributorID
			}
		},
	}
}

func resolveUnit3DSHRITypeID(meta api.PreparedMetadata) string {
	mapping := map[string]string{
		"DISC":   "26",
		"REMUX":  "7",
		"WEBDL":  "27",
		"WEBRIP": "15",
		"HDTV":   "33",
		"ENCODE": "15",
		"DVDRIP": "15",
	}
	return mapping[inferUnit3DType(meta)]
}

func applySHRIDescriptionNotes(description string, meta api.PreparedMetadata) string {
	if !strings.EqualFold(resolveSHRIReleaseGroup(meta), "island") {
		return description
	}
	const shareislandNotes = "Release Shareisland 🏴‍☠️\nFalla girare, condividila e contribuisci a mantenerla viva restando in seed il più possi" + "bile.\nGrazie per il supporto!"
	trimmed := strings.TrimSpace(description)
	if strings.Contains(trimmed, shareislandNotes) {
		return trimmed
	}
	if trimmed == "" {
		return shareislandNotes
	}
	return trimmed + "\n\n" + shareislandNotes
}

func resolveSHRIReleaseGroup(meta api.PreparedMetadata) string {
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
