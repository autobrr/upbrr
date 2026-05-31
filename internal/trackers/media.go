// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

// ReadBDInfo reads the BDInfo from the given path.
func ReadBDInfo(dbPath string, meta api.PreparedMetadata) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: resolve tmp root: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: resolve release tmp dir: %w", err)
	}
	path := paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta))
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	if !existsFile(path) {
		return "", nil
	}
	return readTextFile(path)
}

// ReadBDinfoOrMediaInfo reads the BDInfo if the disc type is BDMV, otherwise it reads the MediaInfoTextPath.
func ReadBDinfoOrMediaInfo(dbPath string, meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, _ := ReadBDInfo(dbPath, meta)
		return strings.TrimSpace(bdinfo)
	}
	return metautil.FirstNonEmptyTrimmed(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), commonhttp.ReadOptionalFile(meta.DVDVOBMediaInfoText))
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
