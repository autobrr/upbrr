// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package paths

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

func BDMVPlaylistKey(playlist string) string {
	return discparse.NormalizePlaylistName(playlist)
}

func BDMVSummaryFilename(playlist string) string {
	key := BDMVPlaylistKey(playlist)
	if key == "" {
		return ""
	}
	return "BD_SUMMARY_" + key + ".txt"
}

func BDMVExtSummaryFilename(playlist string) string {
	key := BDMVPlaylistKey(playlist)
	if key == "" {
		return ""
	}
	return "BD_SUMMARY_EXT_" + key + ".txt"
}

func BDMVFullSummaryFilename(playlist string) string {
	key := BDMVPlaylistKey(playlist)
	if key == "" {
		return ""
	}
	return "BD_SUMMARY_FULL_" + key + ".txt"
}

func BDMVSummaryPath(tmpDir string, playlist string) string {
	filename := BDMVSummaryFilename(playlist)
	if filename == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(tmpDir), filename)
}

func BDMVExtSummaryPath(tmpDir string, playlist string) string {
	filename := BDMVExtSummaryFilename(playlist)
	if filename == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(tmpDir), filename)
}

func BDMVFullSummaryPath(tmpDir string, playlist string) string {
	filename := BDMVFullSummaryFilename(playlist)
	if filename == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(tmpDir), filename)
}

func PrimaryBDMVPlaylist(meta api.PreparedMetadata) string {
	if len(meta.SelectedBDMVPlaylists) == 0 {
		return ""
	}
	return BDMVPlaylistKey(meta.SelectedBDMVPlaylists[0].File)
}

func PrimaryBDMVSummaryPath(tmpRoot string, meta api.PreparedMetadata) (string, error) {
	playlist := PrimaryBDMVPlaylist(meta)
	if playlist == "" {
		return "", nil
	}
	tmpDir, _, err := ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	path := BDMVSummaryPath(tmpDir, playlist)
	if strings.TrimSpace(path) == "" {
		return "", errors.New("paths: missing primary bdmv playlist summary path")
	}
	return path, nil
}

func PrimaryBDMVExtSummaryPath(tmpRoot string, meta api.PreparedMetadata) (string, error) {
	playlist := PrimaryBDMVPlaylist(meta)
	if playlist == "" {
		return "", nil
	}
	tmpDir, _, err := ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	path := BDMVExtSummaryPath(tmpDir, playlist)
	if strings.TrimSpace(path) == "" {
		return "", errors.New("paths: missing primary bdmv ext summary path")
	}
	return path, nil
}

func PrimaryBDMVFullSummaryPath(tmpRoot string, meta api.PreparedMetadata) (string, error) {
	playlist := PrimaryBDMVPlaylist(meta)
	if playlist == "" {
		return "", nil
	}
	tmpDir, _, err := ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	path := BDMVFullSummaryPath(tmpDir, playlist)
	if strings.TrimSpace(path) == "" {
		return "", errors.New("paths: missing primary bdmv full summary path")
	}
	return path, nil
}
