// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fld

import (
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaInfo(dbPath string, meta api.UploadSubject) (string, error) {
	media := trackers.ReadBDinfoOrMediaInfo(dbPath, meta)
	if strings.TrimSpace(media) == "" {
		return "", errors.New("trackers: FLD missing mediainfo")
	}
	return media, nil
}

func buildDiscSection(meta api.UploadSubject, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "DVD":
		media := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), trackers.ReadBDinfoOrMediaInfo(dbPath, meta))
		if media == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=VOB MediaInfo][code]%s[/code][/spoiler]", media)
	case "BDMV":
		bdinfo, _ := trackers.ReadBDInfo(dbPath, meta)
		if bdinfo == "" {
			return ""
		}
		return fmt.Sprintf("[spoiler=BDINFO][code]%s[/code][/spoiler]", bdinfo)
	default:
		return ""
	}
}
