// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveTrackersWithRegistry resolves trackers against composed descriptors.
func ResolveTrackersWithRegistry(cfg config.Config, override []string, remove []string, logger api.Logger, registry *Registry) []string {
	resolved := resolveTrackers(cfg, override, remove)
	resolved = filterKnownTrackersWithRegistry(resolved, logger, registry)
	for i, tracker := range resolved {
		resolved[i] = strings.ToUpper(strings.TrimSpace(tracker))
	}
	return resolved
}

// ResolveExplicitTrackersWithRegistry validates an already-expanded tracker
// selection without falling back to configured defaults. An explicit empty
// selection stays empty.
func ResolveExplicitTrackersWithRegistry(override []string, logger api.Logger, registry *Registry) []string {
	resolved := filterKnownTrackersWithRegistry(append([]string(nil), override...), logger, registry)
	seen := make(map[string]struct{}, len(resolved))
	result := make([]string, 0, len(resolved))
	for _, tracker := range resolved {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

// ResolveTrackersWithDefaultsAndRegistry resolves default and explicit trackers against composed descriptors.
func ResolveTrackersWithDefaultsAndRegistry(cfg config.Config, override []string, remove []string, logger api.Logger, registry *Registry) []string {
	resolved := resolveTrackersWithDefaults(cfg, override, remove)
	resolved = filterKnownTrackersWithRegistry(resolved, logger, registry)
	for i, tracker := range resolved {
		resolved[i] = strings.ToUpper(strings.TrimSpace(tracker))
	}
	return resolved
}
