// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func typeID(meta api.UploadSubject) string {
	if strings.EqualFold(unit3d.Category(meta), "TV") {
		if meta.TVPack {
			return "21"
		}
		return "25"
	}
	if unit3d.Resolution(meta) == "2160p" {
		return "20"
	}
	return "12"
}
