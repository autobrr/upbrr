// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// typeID maps prepared release facts to RMC's disc/remux/encode/broadcast type
// table, which splits Blu-ray and DVD remuxes and disc types unlike the
// standard Unit3D table.
func typeID(meta api.UploadSubject) string {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	source := strings.ToUpper(strings.TrimSpace(meta.Source))
	inferredType := strings.ToUpper(unit3d.InferType(meta))

	switch {
	case discType == "BDMV":
		return "1"
	case inferredType == "REMUX" && (source == "BLURAY" || source == "BLU-RAY"):
		return "2"
	case discType == "DVD":
		return "3"
	case inferredType == "REMUX" && (source == "DVD" || source == "PAL DVD" || source == "NTSC DVD"):
		return "4"
	case inferredType == "ENCODE":
		return "5"
	case inferredType == "DVDRIP":
		return "6"
	case inferredType == "WEBDL":
		return "7"
	case inferredType == "WEBRIP":
		return "8"
	case source == "UHDTV":
		return "9"
	case inferredType == "HDTV":
		return "10"
	case unit3d.Category(meta) == "TV" && unit3d.IsSDResolution(unit3d.Resolution(meta)):
		return "11"
	default:
		return "0"
	}
}

// resolutionID maps prepared release facts to RMC's resolution table. Unknown
// resolutions use RMC's dedicated "Other" ID rather than the standard
// Unit3D fallback.
func resolutionID(meta api.UploadSubject) string {
	if value, ok := map[string]string{
		"4320p": "1",
		"2160p": "2",
		"1440p": "3",
		"1080p": "3",
		"1080i": "4",
		"720p":  "5",
		"576p":  "6",
		"576i":  "7",
		"480p":  "8",
		"480i":  "9",
	}[unit3d.Resolution(meta)]; ok {
		return value
	}
	return "11"
}
