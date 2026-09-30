// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var dupeSeasonPattern = regexp.MustCompile(`(?i)\bS(\d{1,2})`)

// buildDupeSearchParams binds a TMDB work to the site's full category family and
// optional TV season. Missing work or category scope returns nil. Type, resolution
// and episode-number filters are omitted so the evaluator can compare related variants.
func buildDupeSearchParams(meta api.DuplicateSubject, profile SiteProfile) url.Values {
	tmdbID := meta.Identity.TMDBID
	if tmdbID == 0 {
		return nil
	}

	categoryValue, err := meta.Identity.RequireCategory()
	if err != nil {
		return nil
	}
	categoryIDs := resolveUnit3DCategoryIDs(categoryValue, profile)
	if len(categoryIDs) == 0 {
		return nil
	}

	params := url.Values{}
	params.Set("tmdbId", strconv.Itoa(tmdbID))
	for _, id := range categoryIDs {
		params.Add("categories[]", id)
	}
	params.Set("name", "")
	params.Set("perPage", "100")

	if categoryValue == api.CanonicalCategoryTV {
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
