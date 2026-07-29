// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	if meta.Scene {
		if sceneName := strings.TrimSpace(meta.SceneName); sceneName != "" {
			return sceneName
		}
	}
	return metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.ReleaseNameNoTag, meta.Filename, pathutil.Base(meta.SourcePath))
}
