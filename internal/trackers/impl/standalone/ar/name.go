// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveARName(meta api.UploadSubject) string {
	if meta.Scene {
		if sceneName := strings.TrimSpace(meta.SceneName); sceneName != "" {
			return sceneName
		}
	}
	return trackers.SourceReleaseName(meta)
}

func resolveARSearchName(meta api.UploadSubject) string {
	title, year := resolveARSearchNameParts(meta.Release, "", meta.ProviderMetadata)
	title = trackers.PreferredTitle(meta, title)
	year = trackers.PreferredYear(meta, year)
	if title == "" && !meta.EffectiveMetadata.TitleProvenance.IsManual() {
		title = strings.TrimSpace(meta.ReleaseName)
	}
	if title == "" {
		return ""
	}
	if year > 0 {
		return strings.TrimSpace(title + " " + strconv.Itoa(year))
	}
	return title
}

func resolveARSearchNameFields(release api.ReleaseInfo, releaseName string, metadata api.SourceScopedMetadata) string {
	title, year := resolveARSearchNameParts(release, releaseName, metadata)
	if title == "" {
		return ""
	}
	if year > 0 {
		return strings.TrimSpace(title + " " + strconv.Itoa(year))
	}
	return title
}

func resolveARSearchNameParts(release api.ReleaseInfo, releaseName string, metadata api.SourceScopedMetadata) (string, int) {
	title := ""
	if metadata.TMDB != nil {
		title = strings.TrimSpace(metadata.TMDB.Title)
	}
	if title == "" && metadata.IMDB != nil {
		title = strings.TrimSpace(metadata.IMDB.Title)
	}
	if title == "" && metadata.TVDB != nil {
		title = strings.TrimSpace(metadata.TVDB.NameEnglish)
		if title == "" {
			title = strings.TrimSpace(metadata.TVDB.Name)
		}
	}
	if title == "" {
		title = strings.TrimSpace(release.Title)
	}
	if title == "" {
		title = strings.TrimSpace(releaseName)
	}
	year := release.Year
	if year == 0 && metadata.TMDB != nil {
		year = metadata.TMDB.Year
	}
	if year == 0 && metadata.IMDB != nil {
		year = metadata.IMDB.Year
	}
	if year == 0 && metadata.TVDB != nil {
		year = metadata.TVDB.Year
	}
	return title, year
}
