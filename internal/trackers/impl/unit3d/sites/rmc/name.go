// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	disallowedNameCharsRegex = regexp.MustCompile(`[^A-Za-z0-9 ._+-]+`)
	rmcYearTokenRegex        = regexp.MustCompile(`(^|[^0-9])((?:18|19|20)[0-9]{2})([^0-9]|$)`)
)

// buildName replaces the generated title, AKA, and year prefix with RMC's
// required English TMDB title and TMDB year, then removes rejected characters.
// It returns empty unless the prepared name and current matching TMDB metadata
// contain the values needed for a compliant name.
func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := markerName(meta)
	tmdb := currentRMCTMDB(meta.SourcePath, meta.Identity, meta.ProviderMetadata)
	if name == "" || tmdb == nil || strings.TrimSpace(tmdb.Title) == "" || tmdb.Year <= 0 {
		return ""
	}
	yearMatches := rmcYearTokenRegex.FindAllStringSubmatchIndex(name, -1)
	if len(yearMatches) == 0 {
		return ""
	}
	yearEnd := yearMatches[len(yearMatches)-1][5]
	return sanitizeName(strings.TrimSpace(tmdb.Title) + " " + strconv.Itoa(tmdb.Year) + " " + strings.TrimSpace(name[yearEnd:]))
}

// markerName returns the prepared release name, falling back to its no-tag variant.
func markerName(meta api.UploadSubject) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	return strings.TrimSpace(meta.ReleaseNameNoTag)
}

// currentRMCTMDB returns metadata only when it matches the canonical TMDB ID
// and the current prepared source and identity generation.
func currentRMCTMDB(sourcePath string, identity api.ExternalIdentity, metadata api.SourceScopedMetadata) *api.TMDBMetadata {
	if identity.TMDBID <= 0 || metadata.TMDB == nil || metadata.TMDB.TMDBID != identity.TMDBID || !metadata.IsCurrentFor(sourcePath, identity) {
		return nil
	}
	return metadata.TMDB
}

// sanitizeName removes characters outside RMC's accepted name set and collapses whitespace.
func sanitizeName(name string) string {
	cleaned := disallowedNameCharsRegex.ReplaceAllString(name, "")
	return strings.TrimSpace(strings.Join(strings.Fields(cleaned), " "))
}
