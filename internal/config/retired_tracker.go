// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"maps"
	"strings"
)

func isRemovedTracker(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "THR")
}

func isDeprecatedTrackerField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "url", "pronfoapikey", "pronfo_api_key", "pronforapiid", "pronfo_rapi_id", "pronfotheme", "pronfo_theme":
		return true
	default:
		return false
	}
}

// RemoveRetiredTrackerSettings discards removed tracker settings and selections before
// secret processing or export. It preserves unrelated unsupported extensions and
// does not mutate shared maps or slices, including on a shallow config copy.
func RemoveRetiredTrackerSettings(t *TrackersConfig) {
	t.Trackers = maps.Clone(t.Trackers)
	for name, entry := range t.Trackers {
		if isRemovedTracker(name) {
			delete(t.Trackers, name)
			continue
		}
		entry.Unknown = maps.Clone(entry.Unknown)
		for key := range entry.Unknown {
			if isDeprecatedTrackerField(key) {
				delete(entry.Unknown, key)
			}
		}
		t.Trackers[name] = entry
	}
	defaults := make(CSVList, 0, len(t.DefaultTrackers))
	for _, name := range t.DefaultTrackers {
		if !isRemovedTracker(name) {
			defaults = append(defaults, name)
		}
	}
	t.DefaultTrackers = defaults
	if isRemovedTracker(t.PreferredTracker) {
		t.PreferredTracker = ""
	}
}
