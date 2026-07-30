// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveType(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	if category == api.CanonicalCategoryMovie {
		return "401"
	}
	return "402"
}
