// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "strings"

// BDMVRescanRequiredError reports a partial cached Blu-ray analysis that needs
// caller confirmation before rescanning the selected playlists.
type BDMVRescanRequiredError struct {
	// SourcePath identifies the requested local Blu-ray source.
	SourcePath string
	// SelectedPlaylists contains the complete requested playlist set.
	SelectedPlaylists []string
	// CachedPlaylists contains the playlists already present in the cache.
	CachedPlaylists []string
	// MissingPlaylists contains the selected playlists that require a rescan.
	MissingPlaylists []string
}

// Error returns a stable confirmation-required message without the source path.
func (e *BDMVRescanRequiredError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.MissingPlaylists) == 0 {
		return "BDMV rescan confirmation required"
	}
	return "BDMV rescan confirmation required for playlist(s): " + strings.Join(e.MissingPlaylists, ", ")
}

// PlaylistSelectionRequiredError reports a BDMV preparation without a usable
// direct or remembered playlist selection.
type PlaylistSelectionRequiredError struct {
	// SourcePath identifies the requested local Blu-ray source.
	SourcePath string
}

// Error returns the stable playlist-selection-required message.
func (*PlaylistSelectionRequiredError) Error() string {
	return "Blu-ray playlist selection is required"
}

// InvalidPlaylistSelectionError reports a selected playlist that cannot be
// safely resolved from the current BDMV source.
type InvalidPlaylistSelectionError struct {
	// SourcePath identifies the requested local Blu-ray source.
	SourcePath string
	// Playlist identifies the rejected playlist when one was available.
	Playlist string
	// Reason explains the validation failure and is appended to Error's result.
	Reason string
}

// Error returns the stable invalid-selection message and any non-empty reason.
func (e *InvalidPlaylistSelectionError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "Blu-ray playlist selection is invalid"
	}
	return "Blu-ray playlist selection is invalid: " + strings.TrimSpace(e.Reason)
}
