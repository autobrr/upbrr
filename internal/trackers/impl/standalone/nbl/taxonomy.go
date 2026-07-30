// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveCategoryID(meta api.UploadSubject) int {
	if meta.TVPack {
		return 3
	}
	return 1
}
