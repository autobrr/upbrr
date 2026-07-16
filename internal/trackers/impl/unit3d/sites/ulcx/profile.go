// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns ULCX's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "ULCX",
		BaseURL:      "https://upload.cx",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName: buildName,
		},
		DupePolicy: &trackers.DupePolicy{
			AllowSizeVariance1080: true,
		},
	}
}
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") &&
		(strings.Contains(strings.ToLower(strings.TrimSpace(meta.Edition)), "hybrid") || meta.WebDV) {
		name = strings.Replace(name, "Hybrid ", "", 1)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}
