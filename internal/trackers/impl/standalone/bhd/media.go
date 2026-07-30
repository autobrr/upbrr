// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaDump(meta api.UploadSubject, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text := readBDInfoNoErr(dbPath, meta)
		if text == "" {
			return "", errors.New("trackers: BHD missing BDInfo text; generate or attach BDInfo before uploading")
		}
		return text, nil
	case "DVD":
		text := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
		if text == "" {
			return "", errors.New("trackers: BHD missing DVD MediaInfo text; generate or attach DVD MediaInfo before uploading")
		}
		return text, nil
	default:
		if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
			return "", errors.New("trackers: BHD missing mediainfo text; generate or attach MediaInfo before uploading")
		}
		payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", fmt.Errorf("trackers: BHD read mediainfo: %w", err)
		}
		return string(payload), nil
	}
}

func resolveMediaPath(meta api.UploadSubject, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
			return ""
		}
		tmpRoot, err := db.Subdir(dbPath, "tmp")
		if err != nil {
			return ""
		}
		tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
		if err != nil {
			return ""
		}
		return paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists))
	default:
		return strings.TrimSpace(meta.MediaInfoTextPath)
	}
}
