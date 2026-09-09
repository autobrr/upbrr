// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// PlaylistInfo represents a discovered playlist with an opaque backend ID.
// DiscID scopes duplicate filenames; File and DiscName are display evidence only.
type PlaylistInfo struct {
	ID       string         `json:"id"`
	DiscID   string         `json:"discId"`
	DiscName string         `json:"discName"`
	File     string         `json:"file"`
	Duration float64        `json:"duration"`
	Items    []PlaylistItem `json:"items"`
	Score    float64        `json:"score"`
	Edition  string         `json:"edition"`
}

// PlaylistItem represents a single file reference within a playlist.
type PlaylistItem struct {
	File string `json:"file"`
	Size int64  `json:"size"`
}
