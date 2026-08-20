// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func categoryID(meta api.UploadSubject) string {
	if unit3d.Category(meta) == "TV" && meta.Anime {
		return "3"
	}
	return unit3d.DefaultCategoryID(meta)
}
