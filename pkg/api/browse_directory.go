// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

type BrowseDirectoryRequest struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	// Scope selects "roots" for recursive search or "directory" for a listing.
	// An omitted scope preserves legacy initial-directory behavior.
	Scope string `json:"scope,omitempty"`
	Query string `json:"query,omitempty"`
}

type BrowseDirectoryEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

type BrowseDirectoryResponse struct {
	CurrentPath string                 `json:"currentPath"`
	ParentPath  string                 `json:"parentPath"`
	Mode        string                 `json:"mode"`
	Entries     []BrowseDirectoryEntry `json:"entries"`
	Scope       string                 `json:"scope"`
	// Incomplete means a search stopped at a bound or could not inspect a directory or matching entry.
	Incomplete bool `json:"incomplete,omitempty"`
}
