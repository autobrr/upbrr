// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"errors"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaInfo(req trackers.PreparationInput, meta api.UploadSubject) (string, error) {
	if text := metautil.FirstNonEmptyTrimmed(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText)); text != "" {
		return text, nil
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if bdinfo, _ := trackers.ReadBDInfo(req.Runtime.DBPath, meta); strings.TrimSpace(bdinfo) != "" {
			return strings.TrimSpace(bdinfo), nil
		}
	}
	return "", errors.New("trackers: DC missing mediainfo or BDInfo")
}

func resolveMedia(req trackers.PreparationInput, meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, _ := trackers.ReadBDInfo(req.Runtime.DBPath, meta)
		return strings.TrimSpace(bdinfo)
	}
	return metautil.FirstNonEmptyTrimmed(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
}
