// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"errors"
	"fmt"
	"os"
	"strings"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaFields(meta api.UploadSubject, dbPath string) (map[string]string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, err := resolveBDInfo(meta, dbPath)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(bdinfo) == "" {
			return nil, errors.New("trackers: ANT missing BDInfo text")
		}
		return map[string]string{
			"bdinfo":         bdinfo,
			"container_type": "m2ts",
		}, nil
	}

	if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
		return nil, errors.New("trackers: ANT missing mediainfo text")
	}
	payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
	if err != nil {
		return nil, fmt.Errorf("trackers: ANT read mediainfo: %w", err)
	}
	return map[string]string{"mediainfo": string(payload)}, nil
}

func resolveBDInfo(meta api.UploadSubject, dbPath string) (string, error) {
	if summary := strings.TrimSpace(meta.Disc.Summary); summary != "" {
		return summary, nil
	}

	path, err := resolveBDInfoPath(meta, dbPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("trackers: ANT read BDInfo: %w", err)
	}
	return string(payload), nil
}

func resolveBDInfoPath(meta api.UploadSubject, dbPath string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	path, err := paths.PrimaryBDMVSummaryPathFor(tmpRoot, meta.SourcePath, meta.Release, meta.SelectedBDMVPlaylists)
	if err != nil {
		return path, fmt.Errorf("trackers: ANT resolve BDInfo path: %w", err)
	}
	return path, nil
}
