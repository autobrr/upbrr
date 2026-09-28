// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"net/url"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns AITHER's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:                    "AITHER",
		BaseURL:                 "https://aither.cc",
		Rules:                   Rules(),
		ValidationPolicy:        ValidationPolicy(),
		AudioPolicy:             AudioPolicy(),
		ReleaseNamePolicy:       namePolicy(),
		SourceOnlyImageReusable: sourceOnlyImageReusable,
		Site: unit3d.SiteProfile{
			ApplyAdditionalPayload: additionalPayload,
		},
		DupePolicy: &trackers.DupePolicy{
			ID:         "aither/duplicate/v2",
			EvidenceID: "aither-slots-trumping",
			SearchScope: trackers.DupeSearchScope{
				MaxPages: 100,
			},
			SlotDimensions: []trackers.DupeDimension{
				trackers.DupeDimensionType,
				trackers.DupeDimensionResolution,
				trackers.DupeDimensionHDR,
			},
			PrecedenceRules:     trackers.DirectionalMediaKindRules("aither-slots-trumping", "web_dl", "web_rip"),
			SizeVariancePercent: 20,
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			EndpointPath:  "/api/blacklists/releasegroups",
			RequireAPIKey: true,
		},
		ClaimPolicy: &trackers.ClaimPolicy{
			APIBacked: true,
		},
	}
}

func sourceOnlyImageReusable(rawURL string, records []api.TrackerMetadata) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "wsrv.aither.cc" || strings.HasSuffix(host, ".wsrv.aither.cc") {
		return true
	}
	if host != "wsrv.nl" && !strings.HasSuffix(host, ".wsrv.nl") {
		return false
	}
	for _, record := range records {
		if !strings.EqualFold(record.Tracker, "AITHER") {
			continue
		}
		if slices.Contains(record.ImageURLs, rawURL) || strings.Contains(record.Description, rawURL) {
			return true
		}
	}
	return false
}
