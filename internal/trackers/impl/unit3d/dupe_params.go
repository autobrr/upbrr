// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

var dupeSeasonPattern = regexp.MustCompile(`(?i)\bS(\d{1,2})`)

func buildDupeSearchParams(meta api.DuplicateSubject, tracker string) url.Values {
	tmdbID := meta.Identity.TMDBID
	if tmdbID == 0 {
		return nil
	}

	categoryValue, err := meta.Identity.RequireCategory()
	if err != nil {
		return nil
	}
	category := strings.ToUpper(string(categoryValue))
	categoryID := resolveUnit3DDupeCategoryID(tracker, category)
	if categoryID == "" {
		return nil
	}

	params := url.Values{}
	params.Set("tmdbId", strconv.Itoa(tmdbID))
	params.Set("categories[]", categoryID)
	params.Set("name", "")
	params.Set("perPage", "100")

	if strings.EqualFold(category, "TV") {
		season := resolveSeasonValue(meta)
		if season != "" {
			params.Set("name", " "+season)
		}
		if meta.SeasonInt > 0 {
			params.Set("seasonNumber", strconv.Itoa(meta.SeasonInt))
		}
	}

	return params
}

func resolveUnit3DDupeCategoryID(tracker string, category string) string {
	if strings.EqualFold(tracker, "EMUW") {
		switch strings.ToUpper(strings.TrimSpace(category)) {
		case "MOVIE", "FANRES":
			return "1"
		case "TV":
			return "2"
		default:
			return ""
		}
	}
	return trackerdata.CategoryID(category)
}

func resolveSeasonValue(meta api.DuplicateSubject) string {
	if meta.ReleaseNameOverrides.Season != nil {
		return normalizeSeasonEpisode(*meta.ReleaseNameOverrides.Season)
	}
	if match := dupeSeasonPattern.FindStringSubmatch(meta.ReleaseName); len(match) == 2 {
		return "S" + match[1]
	}
	return ""
}

func normalizeSeasonEpisode(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "S") {
		return upper
	}
	if num, err := strconv.Atoi(trimmed); err == nil {
		return "S" + strconv.Itoa(num)
	}
	return upper
}
