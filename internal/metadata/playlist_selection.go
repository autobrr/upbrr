// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

// resolveBDMVPlaylistSelection resolves a direct or remembered selection only
// against playlists discovered from the current BDMV resource root.
func (s *Service) resolveBDMVPlaylistSelection(ctx context.Context, request preparationstate.Request) ([]api.PlaylistInfo, error) {
	bdmvRoot := strings.TrimSpace(request.Layout.BDMVRoot)
	if bdmvRoot == "" {
		return nil, fmt.Errorf("metadata: BDMV resource root is unavailable: %w", internalerrors.ErrInvalidInput)
	}
	discovered, err := discoverBDMVPlaylists(ctx, bdmvRoot)
	if err != nil {
		return nil, fmt.Errorf("metadata: discover BDMV playlists: %w", err)
	}

	instruction := request.Input.Instructions.Playlist
	selected := append([]string(nil), instruction.Selected...)
	useAll := instruction.UseAll
	if !instruction.Set {
		stored, lookupErr := s.repo.GetPlaylistSelection(ctx, playlistSelectionKey(request.Layout.SourcePath))
		if lookupErr != nil {
			if errors.Is(lookupErr, internalerrors.ErrNotFound) {
				return nil, &api.PlaylistSelectionRequiredError{SourcePath: request.Layout.SourcePath}
			}
			return nil, fmt.Errorf("metadata: load remembered playlist selection: %w", lookupErr)
		}
		selected = append([]string(nil), stored.SelectedPlaylists...)
		useAll = stored.UseAll
	}

	if useAll {
		return apiPlaylists(discovered), nil
	}
	if len(selected) == 0 {
		return nil, &api.PlaylistSelectionRequiredError{SourcePath: request.Layout.SourcePath}
	}

	byName := make(map[string]filesystem.PlaylistInfo, len(discovered))
	for _, playlist := range discovered {
		byName[discparse.NormalizePlaylistName(playlist.File)] = playlist
	}
	seen := make(map[string]struct{}, len(selected))
	result := make([]api.PlaylistInfo, 0, len(selected))
	for _, raw := range selected {
		normalized, validationErr := validatePlaylistName(raw)
		if validationErr != nil {
			return nil, &api.InvalidPlaylistSelectionError{
				SourcePath: request.Layout.SourcePath,
				Playlist:   strings.TrimSpace(raw),
				Reason:     validationErr.Error(),
			}
		}
		if _, exists := seen[normalized]; exists {
			return nil, &api.InvalidPlaylistSelectionError{
				SourcePath: request.Layout.SourcePath,
				Playlist:   normalized,
				Reason:     "duplicate playlist",
			}
		}
		playlist, exists := byName[normalized]
		if !exists {
			return nil, &api.InvalidPlaylistSelectionError{
				SourcePath: request.Layout.SourcePath,
				Playlist:   normalized,
				Reason:     "playlist was not found in the current source",
			}
		}
		seen[normalized] = struct{}{}
		result = append(result, apiPlaylist(playlist))
	}
	return result, nil
}

// validatePlaylistName accepts one local MPLS filename and rejects paths or traversal.
func validatePlaylistName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("playlist name is empty")
	}
	if filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" || strings.ContainsAny(trimmed, `/\\`) || trimmed == "." || trimmed == ".." {
		return "", errors.New("playlist name must be a local filename")
	}
	normalized := discparse.NormalizePlaylistName(trimmed)
	if normalized == "" || !strings.EqualFold(filepath.Ext(normalized), ".MPLS") {
		return "", errors.New("playlist name is invalid")
	}
	return normalized, nil
}

// playlistSelectionKey preserves the repository's slash-normalized source key.
func playlistSelectionKey(sourcePath string) string {
	return filepath.ToSlash(filepath.Clean(sourcePath))
}

// apiPlaylists projects discovered filesystem playlists into detached API values.
func apiPlaylists(values []filesystem.PlaylistInfo) []api.PlaylistInfo {
	result := make([]api.PlaylistInfo, 0, len(values))
	for _, value := range values {
		result = append(result, apiPlaylist(value))
	}
	return result
}

// apiPlaylist projects one filesystem playlist and detaches its item list.
func apiPlaylist(value filesystem.PlaylistInfo) api.PlaylistInfo {
	items := make([]api.PlaylistItem, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, api.PlaylistItem{File: item.File, Size: item.Size})
	}
	return api.PlaylistInfo{
		File:     value.File,
		Duration: value.Duration,
		Items:    items,
		Score:    value.Score,
		Edition:  value.Edition,
	}
}
