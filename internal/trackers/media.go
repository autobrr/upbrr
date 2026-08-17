// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

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

// ReadBDInfo reads the selected BDMV playlist summary from the release-scoped
// temporary directory. Missing selections and missing or unstatable artifacts
// return empty without error; directory-resolution and read failures are returned.
func ReadBDInfo(dbPath string, meta api.UploadSubject) (string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") && len(meta.Disc.Items) > 0 {
		expected, ready := bdInfoCoverage(meta.Disc)
		if expected == 0 || ready != expected || !bdInfoDiscCoverage(meta.Disc) {
			return "", errors.New("trackers: incomplete prepared BDInfo evidence")
		}
		return strings.TrimSpace(meta.Disc.AggregateSummary()), nil
	}
	if summary := strings.TrimSpace(meta.Disc.Summary); summary != "" {
		return summary, nil
	}
	path, err := legacyBDInfoPath(dbPath, meta)
	if err != nil {
		return "", err
	}
	if path == "" || !existsFile(path) {
		return "", nil
	}
	return readTextFile(path)
}

// ReadPrimaryBDInfo returns canonical primary BDMV text and its real artifact
// path. Legacy singular subjects retain release-temp lookup compatibility.
func ReadPrimaryBDInfo(dbPath string, meta api.UploadSubject) (string, string, error) {
	if len(meta.Disc.Items) == 0 {
		path, err := legacyBDInfoPath(dbPath, meta)
		if err != nil {
			return "", "", err
		}
		if path == "" || !existsFile(path) {
			return "", path, nil
		}
		text, err := readTextFile(path)
		return strings.TrimSpace(text), path, err
	}

	item, report, ok := meta.Disc.PrimaryReport()
	if !ok || strings.TrimSpace(report.Summary) == "" {
		return "", "", errors.New("trackers: prepared primary BDInfo evidence is missing")
	}
	for _, disc := range meta.Discs {
		if disc.ID != item.ID {
			continue
		}
		for _, resource := range disc.Reports {
			if resource.Playlist.ID != report.Playlist.ID {
				continue
			}
			path := strings.TrimSpace(resource.SummaryPath)
			if path == "" {
				return "", "", errors.New("trackers: prepared primary BDInfo path is missing")
			}
			return strings.TrimSpace(report.Summary), path, nil
		}
	}
	return "", "", errors.New("trackers: prepared primary BDInfo resource is missing")
}

func legacyBDInfoPath(dbPath string, meta api.UploadSubject) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: resolve tmp root: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: resolve release tmp dir: %w", err)
	}
	return strings.TrimSpace(paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists))), nil
}

// ReadDVDVOBMediaInfo returns deterministic per-disc DVD VOB MediaInfo text.
func ReadDVDVOBMediaInfo(meta api.UploadSubject) string {
	return api.AggregateDVDVOBMediaInfo(meta.Discs, meta.DVDVOBMediaInfoText)
}

// ReadBDinfoOrMediaInfo returns BDMV summary text or the first available general
// or DVD-VOB MediaInfo report. Artifact resolution and read errors are treated
// as missing text.
func ReadBDinfoOrMediaInfo(dbPath string, meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, _ := ReadBDInfo(dbPath, meta)
		return strings.TrimSpace(bdinfo)
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return metautil.FirstNonEmptyTrimmed(ReadDVDVOBMediaInfo(meta), readOptionalTextFile(meta.MediaInfoTextPath))
	}
	return readOptionalTextFile(meta.MediaInfoTextPath)
}

func bdInfoCoverage(facts api.DiscFacts) (int, int) {
	expected := 0
	ready := 0
	for _, disc := range facts.Items {
		for _, report := range disc.Reports {
			expected++
			if strings.TrimSpace(report.Summary) != "" {
				ready++
			}
		}
	}
	return expected, ready
}

func bdInfoDiscCoverage(facts api.DiscFacts) bool {
	for _, disc := range facts.Items {
		if len(disc.Reports) == 0 {
			return false
		}
	}
	return true
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
