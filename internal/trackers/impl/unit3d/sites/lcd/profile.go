// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lcd

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns LCD's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "LCD",
		BaseURL: "https://locadora.cc",
		Site: unit3d.SiteProfile{
			BuildName: buildName,
		},
	}
}
func buildName(meta api.UploadSubject, cfg config.TrackerConfig) string {
	return unit3d.FormatLocalizedName(meta, cfg.TagForCustomRelease)
}
