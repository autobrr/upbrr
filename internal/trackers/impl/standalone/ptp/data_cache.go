// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

// CacheKey describes the effective ID or normalized search request and its
// description/image projection. The caller must hash this credential-bearing value.
func (l *dataLookup) CacheKey(req trackers.DataLookupRequest) any {
	apiUser, apiKey := ptpAPIKeys(l.cfg)
	if apiUser == "" || apiKey == "" {
		return nil
	}
	filter, value := "torrentid", strings.TrimSpace(req.TrackerID)
	if value == "" {
		filter, value = "searchstr", ptpSearchName(req.SearchName)
		if value == "" {
			return nil
		}
	}
	discType := ""
	if !req.OnlyID {
		switch normalized := strings.ToUpper(strings.TrimSpace(req.Meta.DiscType)); normalized {
		case "DVD", "BDMV":
			discType = normalized
		}
	}
	return []any{l.endpoint, apiUser, apiKey, filter, value, req.OnlyID, req.KeepImages, discType}
}
