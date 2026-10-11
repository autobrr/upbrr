// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// BrowseDirectoryRequest selects a host directory listing or a search across authorized roots.
type BrowseDirectoryRequest struct {
	// Path is a host filesystem path; a file path lists its containing directory.
	// Roots scope requires an empty path.
	Path string `json:"path"`
	// Mode defaults to "folder". "file" includes recognized video files alongside directories.
	Mode string `json:"mode"`
	// Scope selects "roots" for a root listing or recursive search, or "directory" for a listing.
	// Omitting it preserves initial-path fallback and automatic root selection.
	Scope string `json:"scope,omitempty"`
	// Query searches entry names recursively in roots scope and is limited to 1024 UTF-8 bytes.
	// An empty query after name normalization returns the roots themselves.
	Query string `json:"query,omitempty"`
}

// BrowseDirectoryEntry describes a selectable or navigable host filesystem path.
type BrowseDirectoryEntry struct {
	Name string `json:"name"`
	// Path preserves the listed host path, including an allowed symlink's spelling.
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	// Size is the filesystem-reported byte size; virtual root entries leave it zero.
	Size int64 `json:"size"`
	// ModifiedAt is UTC RFC3339 when available; virtual root entries leave it empty.
	ModifiedAt string `json:"modifiedAt"`
}

// BrowseDirectoryResponse contains a directory listing, root listing, or bounded recursive matches.
type BrowseDirectoryResponse struct {
	// CurrentPath is the listed host directory and is empty for roots scope.
	CurrentPath string `json:"currentPath"`
	// ParentPath is empty when navigation would leave the authorized root.
	ParentPath string                 `json:"parentPath"`
	Mode       string                 `json:"mode"`
	Entries    []BrowseDirectoryEntry `json:"entries"`
	// Scope describes the returned view, which can be "roots" for an initial directory request.
	Scope string `json:"scope"`
	// Incomplete means a recursive search reached a time, entry, or result limit,
	// or could not inspect a directory or matching entry. Returned matches remain usable.
	Incomplete bool `json:"incomplete,omitempty"`
}
