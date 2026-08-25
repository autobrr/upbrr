// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func firstFile(meta api.UploadSubject) string {
	if len(meta.FileList) > 0 {
		return meta.FileList[0]
	}
	return meta.SourcePath
}

func readBDSummary(meta api.UploadSubject, dbPath string) (string, error) {
	text, err := trackers.ReadBDInfo(dbPath, meta)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP read BDInfo: %w", err)
	}
	return text, nil
}

func readTextFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP read text file: %w", err)
	}
	return string(payload), nil
}

func buildMediaSection(meta api.UploadSubject, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text, err := readBDSummary(meta, dbPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		return "[mediainfo]" + strings.TrimSpace(text) + "[/mediainfo]", nil
	case "DVD":
		text := trackers.ReadDVDVOBMediaInfo(meta)
		if strings.TrimSpace(text) == "" {
			var err error
			text, err = readTextFile(strings.TrimSpace(meta.MediaInfoTextPath))
			if err != nil {
				return "", err
			}
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		return "[mediainfo]" + strings.TrimSpace(text) + "[/mediainfo]", nil
	default:
		text, err := readTextFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		return "[mediainfo]" + strings.TrimSpace(text) + "[/mediainfo]", nil
	}
}
