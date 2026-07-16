// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// Profile returns LST's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "LST",
		BaseURL: "https://lst.gg",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			ApplyAdditionalPayload: additionalPayload,
		},
		DupePolicy: &trackers.DupePolicy{
			TrackTrumpableID:     true,
			MatchDVDReleaseGroup: true,
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			EndpointPath:  "/api/bannedReleaseGroups",
			RequireAPIKey: true,
		},
		ImageHost: &trackers.ImageHostPolicy{
			ConditionalHost:   "lostimg",
			EnableWithLostimg: true,
		},
	}
}

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	if req.TrackerConfig.Draft {
		data["draft_queue_opt_in"] = "1"
	} else {
		data["draft_queue_opt_in"] = "0"
	}
	if editionID, ok := editionID(req.Meta.Edition); ok {
		data["edition_id"] = editionID
	}
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
