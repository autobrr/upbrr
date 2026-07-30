// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveName(meta api.UploadSubject) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	return strings.ReplaceAll(metautil.FirstNonEmptyTrimmed(meta.ReleaseNameClean, meta.ReleaseName, meta.Filename), " ", ".")
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.Anime {
		return metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName)
	}
	return resolveName(meta)
}
