// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import "strings"

const globalImageUsageScope = "global"

func normalizeUsageScope(scope string) string {
	trimmed := strings.TrimSpace(scope)
	if trimmed == "" {
		return globalImageUsageScope
	}
	if strings.EqualFold(trimmed, globalImageUsageScope) {
		return globalImageUsageScope
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "tracker:") {
		tracker := strings.TrimSpace(trimmed[len("tracker:"):])
		if tracker == "" {
			return globalImageUsageScope
		}
		return trackerImageUsageScope(tracker)
	}
	return trimmed
}

func trackerImageUsageScope(tracker string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(tracker))
	if trimmed == "" {
		return globalImageUsageScope
	}
	return "tracker:" + trimmed
}

func usageScopeForHost(registry *Registry, host string) string {
	owner := trackerForOwnedHost(registry, host)
	if owner == "" {
		return globalImageUsageScope
	}
	return trackerImageUsageScope(owner)
}

func trackerForOwnedHost(registry *Registry, host string) string {
	return registry.OwnerForImageHost(host)
}

// TrackerForOwnedImageHost returns the registry owner for an image host.
func TrackerForOwnedImageHost(registry *Registry, host string) string {
	return trackerForOwnedHost(registry, host)
}

// TrackerImageUsageScope returns the normalized image usage scope string for the provided tracker name.
func TrackerImageUsageScope(tracker string) string {
	return trackerImageUsageScope(tracker)
}

func uploadEligibleForTracker(scope string, tracker string) bool {
	scope = normalizeUsageScope(scope)
	if scope == globalImageUsageScope {
		return true
	}
	return scope == trackerImageUsageScope(tracker)
}
