// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package shri

import (
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func typeID(meta api.UploadSubject) string {
	return map[string]string{
		"DISC":   "26",
		"REMUX":  "7",
		"WEBDL":  "27",
		"WEBRIP": "15",
		"HDTV":   "33",
		"ENCODE": "15",
		"DVDRIP": "15",
	}[unit3d.InferType(meta)]
}
