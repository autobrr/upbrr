// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveName(meta api.UploadSubject) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") || strings.EqualFold(strings.TrimSpace(meta.Type), "WEBRIP") ||
		strings.EqualFold(strings.TrimSpace(meta.Type), "ENCODE") {
		name = strings.Replace(name, meta.Audio, strings.Replace(meta.Audio, " ", "", 1), 1)
	}
	name = strings.ReplaceAll(name, " DV ", " DoVi ")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Blu-ray Remux")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.ReplaceAll(name, ":", "")
	return strings.TrimSpace(name)
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.Identity.IMDBID != 0 {
		return resolveName(meta)
	}
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	return strings.TrimSpace(meta.ReleaseName)
}
