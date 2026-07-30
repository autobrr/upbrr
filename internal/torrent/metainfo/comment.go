// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package metainfo defines application-owned torrent metainfo values.
package metainfo

const (
	// UploadCreatedBy identifies torrents uploaded directly by upbrr.
	UploadCreatedBy = "uploaded with upbrr"
	// MkbrrUploadCreatedBy identifies upbrr uploads whose torrent was created by mkbrr.
	MkbrrUploadCreatedBy = UploadCreatedBy + " using mkbrr"
	// UploadCommentFallback is used when no tracker-specific torrent comment is available.
	UploadCommentFallback = "upbrr"
)
