// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "strings"

// CountDistinctScreenshotReferences counts included images after merging aliases
// in all references. A shared selected identity or full URL identifies one image.
func CountDistinctScreenshotReferences(all, included []ScreenshotReference) int {
	identities := make(map[WorkflowFingerprint]WorkflowFingerprint)
	root := func(id WorkflowFingerprint) WorkflowFingerprint {
		for identities[id] != id {
			id = identities[id]
		}
		return id
	}
	fullURLs := make(map[string]WorkflowFingerprint)
	for _, image := range all {
		raw := strings.TrimSpace(image.RawURL)
		if image.ID == "" {
			continue
		}
		if _, exists := identities[image.ID]; !exists {
			identities[image.ID] = image.ID
		}
		if raw == "" {
			continue
		}
		if previous, exists := fullURLs[raw]; exists {
			identities[root(image.ID)] = root(previous)
		} else {
			fullURLs[raw] = image.ID
		}
	}
	seen := make(map[WorkflowFingerprint]bool)
	for _, image := range included {
		if _, exists := identities[image.ID]; exists {
			seen[root(image.ID)] = true
		}
	}
	return len(seen)
}
