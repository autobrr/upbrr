// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// ReadBDInfo reads the selected BDMV playlist summary from the release-scoped
// temporary directory. Missing selections and missing or unstatable artifacts
// return empty without error; directory-resolution and read failures are returned.
func ReadBDInfo(dbPath string, meta api.UploadSubject) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: resolve tmp root: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: resolve release tmp dir: %w", err)
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists))
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	if !existsFile(path) {
		return "", nil
	}
	return readTextFile(path)
}

// ReadBDinfoOrMediaInfo returns BDMV summary text or the first available general
// or DVD-VOB MediaInfo report. Artifact resolution and read errors are treated
// as missing text.
func ReadBDinfoOrMediaInfo(dbPath string, meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, _ := ReadBDInfo(dbPath, meta)
		return strings.TrimSpace(bdinfo)
	}
	return metautil.FirstNonEmptyTrimmed(readOptionalTextFile(meta.MediaInfoTextPath), readOptionalTextFile(meta.DVDVOBMediaInfoText))
}

func readOptionalTextFile(path string) string {
	payload, err := readTextFile(path)
	if err != nil {
		return ""
	}
	return payload
}

// existsFile checks if a file exists.
func existsFile(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	_, err := os.Stat(trimmed)
	return err == nil
}

// readTextFile reads the content of a text file.
func readTextFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: read text file: %w", err)
	}
	return string(payload), nil
}
