// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rmc

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
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
	if name == "" || tmdb == nil || strings.TrimSpace(tmdb.Title) == "" {
		return ""
	}
	title := tmdb.Title
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		title = trackers.PreferredTitle(meta, tmdb.Title)
	}
	if title == "" {
		return ""
	}
	year := tmdb.Year
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		year = trackers.PreferredYear(meta, tmdb.Year)
	}
	yearMatches := rmcYearTokenRegex.FindAllStringSubmatchIndex(name, -1)
	switch {
	case len(yearMatches) == 0 && meta.NamePresentation.Version == api.ReleaseNamePresentationVersionV1 && meta.NamePresentation.OmitYear:
		return buildYearlessName(name, meta, tmdb, title)
	case len(yearMatches) == 0 || year <= 0:
		return ""
	}
	yearEnd := yearMatches[len(yearMatches)-1][5]
	return sanitizeName(strings.TrimSpace(title) + " " + strconv.Itoa(year) + " " + strings.TrimSpace(name[yearEnd:]))
}

func buildYearlessName(name string, meta api.UploadSubject, tmdb *api.TMDBMetadata, title string) string {
	suffix := ""
	for _, candidate := range []string{title, meta.Release.Title, tmdb.Title} {
		if remainder, ok := trimLeadingNameElement(name, candidate); ok {
			suffix = remainder
			break
		}
	}
	if suffix == "" {
		return ""
	}

	alternates := make([]string, 0, 4)
	switch {
	case meta.EffectiveMetadata.AlternateTitleProvenance.IsManual():
		alternates = append(alternates, trackers.PreferredAlternateTitle(meta, ""))
	case meta.EffectiveMetadata.OriginalTitleProvenance.IsManual():
		alternates = append(alternates, trackers.PreferredOriginalTitle(meta, ""))
	default:
		alternates = append(alternates, meta.Release.Alt, tmdb.RetrievedAKA, tmdb.OriginalTitle)
		if imdb := meta.ProviderMetadata.IMDB; imdb != nil {
			alternates = append(alternates, imdb.AKA)
		}
	}
	for _, alternate := range alternates {
		if remainder, ok := trimLeadingNameElement(suffix, alternate); ok {
			suffix = remainder
			break
		}
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(suffix)), "AKA ") {
		return ""
	}
	return sanitizeName(strings.TrimSpace(title) + " " + suffix)
}

func trimLeadingNameElement(name, element string) (string, bool) {
	element = strings.TrimSpace(element)
	if element == "" {
		return name, false
	}
	candidates := []string{element}
	if len(element) > len("AKA ") && strings.EqualFold(element[:len("AKA ")], "AKA ") {
		candidates = append(candidates, strings.TrimSpace(element[len("AKA "):]))
	} else {
		candidates = append(candidates, "AKA "+element)
	}
	for _, candidate := range candidates {
		if len(name) < len(candidate) || !strings.EqualFold(name[:len(candidate)], candidate) {
			continue
		}
		if len(name) > len(candidate) && !strings.ContainsRune(" ._-", rune(name[len(candidate)])) {
			continue
		}
		return strings.TrimSpace(name[len(candidate):]), true
	}
	return name, false
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
