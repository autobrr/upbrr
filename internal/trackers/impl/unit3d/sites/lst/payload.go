// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
)

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	if req.TrackerConfig.Draft {
		data["draft_queue_opt_in"] = "1"
	} else {
		data["draft_queue_opt_in"] = "0"
	}
	if editionID, ok := editionID(req.Meta.Edition); ok {
		data["edition_id"] = editionID
	}
	if hdrDV, ok := mediafacts.Unit3DHDRDVFromFacts(req.Meta.HDRFacts); ok {
		data["hdr_dv"] = hdrDV
	}
	if provider := lstProvider(req.Meta.Service); provider != "" {
		data["provider"] = provider
	}
	if dualAudio, ok := lstDualAudio(req); ok {
		data["dual_audio"] = dualAudio
	}
}

func lstProvider(service string) string {
	service = strings.TrimSpace(service)
	switch strings.ToUpper(service) {
	case "AND":
		//nolint:misspell // ADN is LST's exact provider code.
		return "ADN"
	case "HOTSTAR", "HSTR":
		return "HTSR"
	default:
		return service
	}
}

func lstDualAudio(req trackers.PreparationInput) (string, bool) {
	if value := req.Meta.ReleaseNameOverrides.DualAudio; value != nil {
		if *value {
			return "1", true
		}
		return "0", true
	}
	if value := req.Meta.ReleaseNameOverrides.NoDual; value != nil && *value {
		return "0", true
	}
	audio := strings.ToLower(strings.ReplaceAll(req.Meta.Audio, " ", "-"))
	if strings.Contains(audio, "dual-audio") {
		return "1", true
	}
	return "", false
}

func editionID(edition string) (string, bool) {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(edition)), "’", "'")
	value, ok := map[string]string{
		"collector's edition": "1",
		"director's cut":      "2",
		"extended cut":        "3",
		"extended uncut":      "4",
		"extended unrated":    "5",
		"limited edition":     "6",
		"special edition":     "7",
		"theatrical cut":      "8",
		"uncut":               "9",
		"unrated":             "10",
		"x cut":               "11",
		"alternative cut":     "12",
		"other":               "0",
	}[normalized]
	return value, ok
}
