// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

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
	if IsDVDSource(meta.Source) {
		audio := strings.Join(strings.Fields(strings.TrimSpace(meta.Audio)), " ")
		if audio != "" && strings.TrimSpace(meta.VideoCodec) != "" {
			name = strings.Replace(name, audio, strings.TrimSpace(meta.VideoCodec)+" "+audio, 1)
		}
	}
	return strings.ReplaceAll(name, "DD+", "DDP")
}
