// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strings"

	"github.com/moistari/rls"

	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

func releaseCategoryFromRLS(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	switch upper {
	case "MOVIE":
		return "MOVIE"
	case "EP", "EPS", "EPISODE", "SEASON", "SEASONPACK", "SERIES", "TV", "TVSHOW", "TV-SHOW":
		return "TV"
	default:
		return ""
	}
}

func ParseReleaseInfo(path string) api.ReleaseInfo {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return api.ReleaseInfo{}
	}

	base := pathutil.Base(trimmed)
	if base == "." || base == "/" || base == "" {
		return api.ReleaseInfo{}
	}

	release := rls.ParseString(base)
	season := release.Series
	episode := release.Episode
	if season == 0 || episode == 0 {
		extracted := seasonep.Extract(base, api.PreparedMetadata{})
		if season == 0 {
			season = extracted.Season
		}
		if episode == 0 {
			episode = extracted.Episode
		}
	}

	return api.ReleaseInfo{
		Category:   releaseCategoryFromRLS(release.Type.String()),
		Type:       release.Source,
		Artist:     release.Artist,
		Title:      release.Title,
		Subtitle:   release.Subtitle,
		Alt:        release.Alt,
		Year:       release.Year,
		Month:      release.Month,
		Day:        release.Day,
		Source:     release.Source,
		Resolution: release.Resolution,
		Codec:      append([]string{}, release.Codec...),
		Audio:      append([]string{}, release.Audio...),
		HDR:        append([]string{}, release.HDR...),
		Ext:        release.Ext,
		Language:   append([]string{}, release.Language...),
		Site:       release.Site,
		Genre:      release.Genre,
		Channels:   release.Channels,
		Collection: release.Collection,
		Region:     release.Region,
		Size:       release.Size,
		Group:      release.Group,
		Disc:       release.Disc,
		Season:     season,
		Episode:    episode,
		Edition:    append([]string{}, release.Edition...),
		Other:      append([]string{}, release.Other...),
	}
}
