// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func ResolveTrackers(cfg config.Config, override []string, remove []string, logger api.Logger) []string {
	resolved := resolveTrackers(cfg, override, remove)
	resolved = filterKnownTrackers(resolved, logger)
	for i, tracker := range resolved {
		resolved[i] = strings.ToUpper(strings.TrimSpace(tracker))
	}
	return resolved
}

func ResolveTrackersWithDefaults(cfg config.Config, override []string, remove []string, logger api.Logger) []string {
	resolved := resolveTrackersWithDefaults(cfg, override, remove)
	resolved = filterKnownTrackers(resolved, logger)
	for i, tracker := range resolved {
		resolved[i] = strings.ToUpper(strings.TrimSpace(tracker))
	}
	return resolved
}

// ResolveIMDbIDText returns the IMDb ID in "tt1234567" format from the metadata,
// preferring the canonical text form over a numeric fallback.
func ResolveIMDbIDText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}
