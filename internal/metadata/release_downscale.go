// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/rls"

	"github.com/autobrr/upbrr/pkg/api"
)

// parsedDownscaleMarker retains explicit technical-suffix evidence only.
// Titles, episode titles, groups and output resolution cannot establish it.
// The boolean is false when the same token also occurs before technical facts.
func parsedDownscaleMarker(release rls.Release) (string, bool) {
	technical, titleMarker := false, false
	for _, tag := range release.Tags() {
		if tag.Is(rls.TagTypeGroup, rls.TagTypeExt) {
			break
		}
		if tag.Is(rls.TagTypeSource, rls.TagTypeResolution) {
			technical = true
		}
		if strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%o", tag)), "DS4K") {
			if technical {
				return "DS4K", !titleMarker
			}
			titleMarker = true
		}
	}
	return "", false
}

func downscaleFromRelease(release api.ReleaseInfo) string {
	if slices.Contains(release.Other, "DS4K") {
		return "DS4K"
	}
	return ""
}
