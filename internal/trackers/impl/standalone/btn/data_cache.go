// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

// CacheKey describes the ID request using the same current or legacy token as
// Lookup. The caller must hash this credential-bearing value.
func (l *dataLookup) CacheKey(req trackers.DataLookupRequest) any {
	token := strings.TrimSpace(config.ResolveBTNAPIToken(l.cfg))
	id := strings.TrimSpace(req.TrackerID)
	if len(token) < 25 || id == "" {
		return nil
	}
	return []any{l.endpoint, token, id}
}
