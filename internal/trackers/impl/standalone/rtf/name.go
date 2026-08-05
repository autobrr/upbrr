// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	return metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename)
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.Identity.IMDBID != 0 {
		return resolveUploadName(meta)
	}
	query := strings.TrimSpace(meta.Release.Title)
	if query == "" {
		query = strings.TrimSpace(meta.ReleaseName)
	}
	query = strings.NewReplacer(":", " ", ",", "", "'", "", "’", "").Replace(query)
	return strings.Join(strings.Fields(query), " ")
}
