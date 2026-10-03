// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
)

// CacheKey describes the effective lookup and description/image demands. RSS
// credentials gate every lookup but are sent only for searches. The caller must
// hash this credential-bearing value.
func (l *dataLookup) CacheKey(req trackers.DataLookupRequest) any {
	cfg, apiKey := bhdConfig(l.cfg)
	rssKey := strings.TrimSpace(cfg.BhdRSSKey)
	if len(apiKey) < minDataTokenLength || len(rssKey) < minDataTokenLength {
		return nil
	}
	endpoint := strings.TrimRight(l.baseURL, "/") + "/" + apiKey
	if id := strings.TrimSpace(req.TrackerID); id != "" {
		return []any{endpoint, "details", id, req.OnlyID, req.KeepImages}
	}
	filter, value := "file_name", strings.TrimSpace(req.SearchName)
	if strings.TrimSpace(req.Meta.DiscType) != "" || len(req.Meta.FileList) != 1 {
		filter, value = "folder_name", pathutil.Base(req.Meta.SourcePath)
	} else if value == "" {
		return nil
	}
	return []any{endpoint, "search", rssKey, filter, value, req.OnlyID, req.KeepImages}
}
