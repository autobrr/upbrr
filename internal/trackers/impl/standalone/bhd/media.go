// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaDump(meta api.UploadSubject, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text, _, err := trackers.ReadPrimaryBDInfo(dbPath, meta)
		if err != nil {
			return "", fmt.Errorf("trackers: BHD primary BDInfo: %w", err)
		}
		if text == "" {
			return "", errors.New("trackers: BHD missing BDInfo text; generate or attach BDInfo before uploading")
		}
		return text, nil
	case "DVD":
		text := metautil.FirstNonEmptyTrimmed(trackers.ReadDVDVOBMediaInfo(meta), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
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
		_, path, _ := trackers.ReadPrimaryBDInfo(dbPath, meta)
		return path
	default:
		return strings.TrimSpace(meta.MediaInfoTextPath)
	}
}
