// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package metainfo defines application-owned torrent metainfo values.
package metainfo

import "strings"

const (
	// UploadCreatedBy identifies torrents uploaded directly by upbrr.
	UploadCreatedBy = "upbrr"
	// MkbrrUploadCreatedBy identifies upbrr uploads whose torrent was created by mkbrr.
	MkbrrUploadCreatedBy = UploadCreatedBy + " with mkbrr"
	// UploadComment identifies torrents uploaded with upbrr.
	UploadComment = "uploaded with upbrr"
)

// CanonicalCreatedBy preserves mkbrr provenance while applying upbrr's creator value.
func CanonicalCreatedBy(createdBy string) string {
	if strings.Contains(strings.ToLower(createdBy), "mkbrr") {
		return MkbrrUploadCreatedBy
	}
	return UploadCreatedBy
}
