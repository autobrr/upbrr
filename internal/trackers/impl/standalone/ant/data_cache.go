// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
)

// CacheKey describes the filename request; ANT does not use tracker IDs or
// description/image demands. The caller must hash this credential-bearing value.
func (l *dataLookup) CacheKey(req trackers.DataLookupRequest) any {
	apiKey := antAPIKey(l.cfg)
	fileName := strings.TrimSpace(req.SearchName)
	if strings.TrimSpace(req.Meta.DiscType) != "" || apiKey == "" || fileName == "" {
		return nil
	}
	return []any{l.endpoint, apiKey, fileName}
}
