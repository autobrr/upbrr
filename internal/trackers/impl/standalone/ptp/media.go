// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"fmt"
	"os"
	"strings"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func firstFile(meta api.UploadSubject) string {
	if len(meta.FileList) > 0 {
		return meta.FileList[0]
	}
	return meta.SourcePath
}

func readBDSummary(meta api.UploadSubject, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return readTextFile(paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists)))
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
