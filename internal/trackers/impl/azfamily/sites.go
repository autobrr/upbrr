// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
)

type siteDefinition struct {
	Name               string
	BaseURL            string
	RequestsURL        string
	DefaultAnnounceURL string
	SourceFlag         string
	InternalTagID      string
	PersonalReleaseTag string
}

func siteFor(name string) siteDefinition {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "CZ":
		return siteDefinition{
			Name:               "CZ",
			BaseURL:            "https://cinemaz.to",
			RequestsURL:        "https://cinemaz.to/requests",
			DefaultAnnounceURL: "https://tracker.cinemaz.to/announce",
			SourceFlag:         "CinemaZ",
			InternalTagID:      "938",
			PersonalReleaseTag: "1594",
		}
	case "PHD":
		return siteDefinition{
			Name:               "PHD",
			BaseURL:            "https://privatehd.to",
			RequestsURL:        "https://privatehd.to/requests",
			DefaultAnnounceURL: "https://tracker.privatehd.to/announce",
			SourceFlag:         "PrivateHD",
			InternalTagID:      "415",
			PersonalReleaseTag: "1448",
		}
	default:
		return siteDefinition{
			Name:               "AZ",
			BaseURL:            "https://avistaz.to",
			RequestsURL:        "https://avistaz.to/requests",
			DefaultAnnounceURL: "https://tracker.avistaz.to/announce",
			SourceFlag:         "AvistaZ",
			InternalTagID:      "943",
			PersonalReleaseTag: "3773",
		}
	}
}

func applyTrackerConfig(site siteDefinition, _ config.TrackerConfig) siteDefinition {
	return site
}
