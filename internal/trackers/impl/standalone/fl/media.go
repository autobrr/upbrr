// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMedia(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		text, _ := trackers.ReadBDInfo("", meta)
		return strings.TrimSpace(text)
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return metautil.FirstNonEmptyTrimmed(trackers.ReadDVDVOBMediaInfo(meta), commonhttp.ReadOptionalFile(meta.MediaInfoTextPath))
	}
	return commonhttp.ReadOptionalFile(meta.MediaInfoTextPath)
}
