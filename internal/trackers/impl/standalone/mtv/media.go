// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"fmt"
	"os"
	"strings"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildMediaInfoBlock(meta api.UploadSubject, dbPath string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdInfoPath, err := resolveBDInfoPath(meta, dbPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(bdInfoPath) != "" {
			text, err := os.ReadFile(bdInfoPath)
			if err != nil {
				if !os.IsNotExist(err) {
					return "", fmt.Errorf("description: MTV read BDInfo file: %w", err)
				}
			} else {
				trimmed := strings.TrimSpace(string(text))
				if trimmed != "" {
					return "[mediainfo]" + trimmed + "[/mediainfo]", nil
				}
			}
		}
	}

	if strings.TrimSpace(meta.MediaInfoTextPath) != "" {
		text, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("description: MTV read MediaInfo file: %w", err)
			}
		} else {
			trimmed := strings.TrimSpace(string(text))
			if trimmed != "" {
				return "[mediainfo]" + trimmed + "[/mediainfo]", nil
			}
		}
	}

	return "", nil
}

func resolveBDInfoPath(meta api.UploadSubject, dbPath string) (string, error) {
	if strings.TrimSpace(dbPath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("description: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("description: %w", err)
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists))
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("description: MTV stat BDInfo summary: %w", err)
	}
	return path, nil
}
