// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
)

// CacheKey describes the effective ID or folder/file request and its projections.
// The caller must hash this credential-bearing value.
func (l *dataLookup) CacheKey(req trackers.DataLookupRequest) any {
	username, passkey := hdbCredentials(l.cfg)
	if username == "" || passkey == "" {
		return nil
	}
	filter, value := "id", strings.TrimSpace(req.TrackerID)
	if value == "" {
		filter, value = "file_in_torrent", strings.TrimSpace(req.SearchName)
		if strings.TrimSpace(req.Meta.DiscType) != "" || len(req.Meta.FileList) != 1 {
			filter, value = "search", pathutil.Base(req.Meta.SourcePath)
		} else if value == "" {
			return nil
		}
	}
	return []any{l.endpoint, username, passkey, filter, value, req.OnlyID, req.KeepImages}
}
