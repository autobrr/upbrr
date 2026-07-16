// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/moistari/rls"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

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
	typeValue := parsedReleaseType(base, release.Source, release.Other, release.Codec)
	sourceValue := parsedReleaseSource(base, release.Source, typeValue)
	groupValue := parsedReleaseGroup(base, release.Group, release.Site)
	season := release.Series
	episode := release.Episode
	if season == 0 || episode == 0 {
		extracted := seasonep.Extract(base, preparationstate.State{})
		if season == 0 {
			season = extracted.Season
		}
		if episode == 0 {
			episode = extracted.Episode
		}
	}

	// Category and Type intentionally come from different sources: Category is
	// the movie/TV content class from the RLS parser, while Type is the derived
	// release format (for example WEBDL, REMUX, or ENCODE) used elsewhere.
	return api.ReleaseInfo{
		Category:   metautil.ReleaseCategoryFromRLS(release.Type.String()),
		Type:       typeValue,
		Artist:     release.Artist,
		Title:      release.Title,
		Subtitle:   release.Subtitle,
		Alt:        release.Alt,
		Year:       release.Year,
		Month:      release.Month,
		Day:        release.Day,
		Source:     sourceValue,
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
		Group:      groupValue,
		Disc:       release.Disc,
		Season:     season,
		Episode:    episode,
		Edition:    append([]string{}, release.Edition...),
		Other:      append([]string{}, release.Other...),
	}
}

func parsedReleaseType(base string, source string, other []string, codec []string) string {
	if inferred := webReleaseTypeFromSignals("", source, base); inferred != "" {
		return inferred
	}
	for _, value := range other {
		if strings.EqualFold(strings.TrimSpace(value), "REMUX") {
			return "REMUX"
		}
	}
	for _, value := range codec {
		switch strings.ToUpper(strings.TrimSpace(value)) {
		case "X264", "X265", "XVID", "DIVX":
			return "ENCODE"
		}
	}
	return ""
}

func parsedReleaseSource(base string, source string, typeValue string) string {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		trimmed = inferReleaseSourceFromName(base, typeValue)
	}
	switch normalizeReleaseType(typeValue) {
	case "WEBDL", "WEBRIP":
		return "Web"
	}
	if strings.EqualFold(trimmed, "Ultra HDTV") {
		return "UHDTV"
	}
	return trimmed
}

// parsedReleaseGroup preserves explicit parser groups and treats a leading
// bracket site token as the group when no explicit suffix group was parsed.
func parsedReleaseGroup(base string, group string, site string) string {
	trimmedGroup := strings.TrimSpace(group)
	if trimmedGroup != "" {
		return trimmedGroup
	}
	trimmedSite := strings.TrimSpace(site)
	if trimmedSite == "" {
		return ""
	}
	if !strings.HasPrefix(strings.TrimSpace(base), "["+trimmedSite+"]") {
		return ""
	}
	return trimmedSite
}
