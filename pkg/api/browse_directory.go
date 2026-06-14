// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

type BrowseDirectoryRequest struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
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
}
