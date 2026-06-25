// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveTrackers returns known trackers from explicit overrides when provided,
// otherwise configured defaults, after applying removals. Returned names are
// trimmed and uppercased.
func ResolveTrackers(cfg config.Config, override []string, remove []string, logger api.Logger) []string {
	resolved := resolveTrackers(cfg, override, remove)
	resolved = filterKnownTrackers(resolved, logger)
	for i, tracker := range resolved {
		resolved[i] = strings.ToUpper(strings.TrimSpace(tracker))
	}
	return resolved
}

// ResolveTrackersWithDefaults returns known configured default trackers plus
// explicit overrides, after applying removals. Use it for flows where explicit
// tracker selections augment defaults instead of replacing them. Returned names
// are trimmed and uppercased.
func ResolveTrackersWithDefaults(cfg config.Config, override []string, remove []string, logger api.Logger) []string {
	resolved := resolveTrackersWithDefaults(cfg, override, remove)
	resolved = filterKnownTrackers(resolved, logger)
	for i, tracker := range resolved {
		resolved[i] = strings.ToUpper(strings.TrimSpace(tracker))
	}
	return resolved
}
