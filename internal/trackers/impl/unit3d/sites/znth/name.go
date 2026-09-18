// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package znth

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/znth/v2", trackers.StructuredNamePolicy{
		Defaults: func(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
			if unit3d.Category(meta) != "TV" {
				return nil
			}
			return editor.Omit(api.NameRoleEpisodeTitle)
		},
	})
}
