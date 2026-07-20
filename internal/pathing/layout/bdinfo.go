// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package layout

import (
	"errors"
	"path/filepath"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

// BDMVPlaylistKey returns the normalized playlist identifier used in BDInfo artifact names.
func BDMVPlaylistKey(playlist string) string {
	return discparse.NormalizePlaylistName(playlist)
}

// BDMVSummaryFilename returns the standard BDInfo summary filename for playlist.
func BDMVSummaryFilename(playlist string) string {
	key := BDMVPlaylistKey(playlist)
	if key == "" {
		return ""
	}
	return "BD_SUMMARY_" + key + ".txt"
}

// BDMVExtSummaryFilename returns the extended BDInfo summary filename for playlist.
func BDMVExtSummaryFilename(playlist string) string {
	key := BDMVPlaylistKey(playlist)
	if key == "" {
		return ""
	}
	return "BD_SUMMARY_EXT_" + key + ".txt"
}

// BDMVFullSummaryFilename returns the full BDInfo summary filename for playlist.
func BDMVFullSummaryFilename(playlist string) string {
	key := BDMVPlaylistKey(playlist)
	if key == "" {
		return ""
	}
	return "BD_SUMMARY_FULL_" + key + ".txt"
}

// BDMVSummaryPath returns the standard BDInfo summary path beneath tmpDir.
func BDMVSummaryPath(tmpDir string, playlist string) string {
	filename := BDMVSummaryFilename(playlist)
	if filename == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(tmpDir), filename)
}

// BDMVExtSummaryPath returns the extended BDInfo summary path beneath tmpDir.
func BDMVExtSummaryPath(tmpDir string, playlist string) string {
	filename := BDMVExtSummaryFilename(playlist)
	if filename == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(tmpDir), filename)
}

// BDMVFullSummaryPath returns the full BDInfo summary path beneath tmpDir.
func BDMVFullSummaryPath(tmpDir string, playlist string) string {
	filename := BDMVFullSummaryFilename(playlist)
	if filename == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(tmpDir), filename)
}

// PrimaryBDMVPlaylist returns the primary playlist selected in meta.
func PrimaryBDMVPlaylist(meta preparationstate.State) string {
	return PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists)
}

// PrimaryBDMVPlaylistFor returns the normalized primary playlist from a
// narrow playlist selection.
func PrimaryBDMVPlaylistFor(playlists []api.PlaylistInfo) string {
	if len(playlists) == 0 {
		return ""
	}
	return BDMVPlaylistKey(playlists[0].File)
}

// PrimaryBDMVSummaryPath resolves the standard summary path for meta's primary playlist.
func PrimaryBDMVSummaryPath(tmpRoot string, meta preparationstate.State) (string, error) {
	return PrimaryBDMVSummaryPathFor(tmpRoot, meta.SourcePath, meta.Release, meta.SelectedBDMVPlaylists)
}

// PrimaryBDMVSummaryPathFor resolves the standard summary path from narrow
// source, naming, and playlist facts.
func PrimaryBDMVSummaryPathFor(tmpRoot string, source string, release api.ReleaseInfo, playlists []api.PlaylistInfo) (string, error) {
	playlist := PrimaryBDMVPlaylistFor(playlists)
	if playlist == "" {
		return "", nil
	}
	tmpDir, _, err := ReleaseTempDirFor(tmpRoot, source, release)
	if err != nil {
		return "", err
	}
	path := BDMVSummaryPath(tmpDir, playlist)
	if strings.TrimSpace(path) == "" {
		return "", errors.New("paths: missing primary bdmv playlist summary path")
	}
	return path, nil
}

// PrimaryBDMVExtSummaryPath resolves the extended summary path for meta's primary playlist.
func PrimaryBDMVExtSummaryPath(tmpRoot string, meta preparationstate.State) (string, error) {
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

// PrimaryBDMVFullSummaryPath resolves the full summary path for meta's primary playlist.
func PrimaryBDMVFullSummaryPath(tmpRoot string, meta preparationstate.State) (string, error) {
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
