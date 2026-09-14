// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	name := metautil.FirstNonEmptyTrimmed(
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.ReleaseNameNoTag),
		strings.TrimSpace(meta.Filename),
		pathutil.Base(meta.SourcePath),
	)
	replacer := strings.NewReplacer(" ", ".", ":.", ".", ":", ".", "DD+", "DDP")
	normalized := replacer.Replace(name)
	for strings.Contains(normalized, "..") {
		normalized = strings.ReplaceAll(normalized, "..", ".")
	}
	return strings.Trim(normalized, ".")
}
