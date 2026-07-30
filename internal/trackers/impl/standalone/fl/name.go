// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveName(meta api.UploadSubject, answers map[string]string) string {
	if answers != nil && strings.TrimSpace(answers["name"]) != "" {
		return strings.TrimSpace(answers["name"])
	}
	name := strings.TrimSpace(meta.ReleaseName)
	name = strings.ReplaceAll(name, " DV ", " DoVi ")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Remux")
	name = strings.ReplaceAll(name, "BluRay Remux", "Remux")
	name = strings.ReplaceAll(name, "PQ10", "HDR")
	name = strings.ReplaceAll(name, "HDR10+", "HDR")
	name = strings.ReplaceAll(name, "DD+", "DDP")
	name = strings.Join(strings.Fields(name), ".")
	return strings.Trim(name, ".")
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.Identity.IMDBID != 0 {
		return resolveName(meta, nil)
	}
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	return strings.TrimSpace(meta.ReleaseName)
}
