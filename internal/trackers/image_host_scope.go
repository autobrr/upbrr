// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import "strings"

const globalImageUsageScope = "global"

var trackerOwnedImageHosts = map[string]string{
	"hdb": "HDB",
}

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

func usageScopeForHost(tracker string, host string) string {
	owner := trackerForOwnedHost(host)
	if owner == "" {
		return globalImageUsageScope
	}
	if strings.EqualFold(owner, tracker) {
		return trackerImageUsageScope(owner)
	}
	return trackerImageUsageScope(owner)
}

func trackerForOwnedHost(host string) string {
	normalized := strings.ToLower(strings.TrimSpace(host))
	return trackerOwnedImageHosts[normalized]
}

func TrackerForOwnedImageHost(host string) string {
	return trackerForOwnedHost(host)
}

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
