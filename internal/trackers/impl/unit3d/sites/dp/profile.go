// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns DP's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:         "DP",
		BaseURL:      "https://darkpeers.org",
		Rules:        Rules(),
		BannedGroups: BannedGroups(),
		Site: unit3d.SiteProfile{
			BuildName: buildName,
		},
	}
}

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := baseName(meta)
	if label := audioLabel(meta.AudioLanguages); label != "" {
		name = strings.Replace(name, "Dual-Audio", label, 1)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}
func baseName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}
func audioLabel(values []string) string {
	unique := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			unique[strings.ToUpper(value)] = struct{}{}
		}
	}
	switch len(unique) {
	case 0:
		return ""
	case 1:
		for value := range unique {
			return value
		}
		return ""
	case 2:
		return "Dual-Audio"
	default:
		return "MULTi"
	}
}
