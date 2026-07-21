// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func normalizeTracker(tracker string) string {
	return strings.ToUpper(strings.TrimSpace(tracker))
}

func trimEntries(entries []api.DupeEntry) []api.DupeEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]api.DupeEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Name = strings.TrimSpace(entry.Name)
		entry.Link = strings.TrimSpace(entry.Link)
		entry.Download = strings.TrimSpace(entry.Download)
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Type = strings.TrimSpace(entry.Type)
		entry.Res = strings.TrimSpace(entry.Res)
		if entry.Name == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}
