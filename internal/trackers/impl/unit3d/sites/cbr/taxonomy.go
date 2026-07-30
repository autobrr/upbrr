// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cbr

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func categoryID(meta api.UploadSubject) string {
	if strings.EqualFold(unit3d.Category(meta), "TV") && meta.Anime {
		return "4"
	}
	return unit3d.DefaultCategoryID(meta)
}
