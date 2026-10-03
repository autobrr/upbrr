// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"errors"
	"fmt"
	"io"
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

// Match the size bound used when scene metadata downloads an NFO.
const maxNFOBytes = 8 << 20

// resolveNFO captures the prepared local NFO without changing its formatting.
// An absent path omits the optional NFO; unreadable or oversized content fails preparation.
func resolveNFO(meta api.UploadSubject) (string, error) {
	path := strings.TrimSpace(meta.SceneNFOPath)
	if path == "" {
		return "", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("trackers: BHD open NFO: %w", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxNFOBytes+1))
	if err != nil {
		return "", fmt.Errorf("trackers: BHD read NFO: %w", err)
	}
	if len(payload) > maxNFOBytes {
		return "", errors.New("trackers: BHD NFO exceeds size limit")
	}
	return string(payload), nil
}
