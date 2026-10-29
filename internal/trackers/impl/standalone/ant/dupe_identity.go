// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"slices"
	"strconv"

	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func antCategories(item map[string]any) []int {
	values, ok := item["category"].([]any)
	if !ok {
		values = []any{item["category"]}
	}
	categories := make([]int, 0, len(values))
	for _, value := range values {
		categories = append(categories, int(antInt(value)))
	}
	return categories
}

func antCategory(item map[string]any) string {
	for _, category := range antCategories(item) {
		switch {
		case category == 3 || category == 4 || category == 100003 || category == 100004 || category >= 5000 && category < 6000:
			return "tv"
		case category == 1 || category == 2 || category == 100001 || category == 100002 || category >= 2000 && category < 3000:
			return "movie"
		}
	}
	return ""
}

func antSeasonCategory(item map[string]any) bool {
	categories := antCategories(item)
	return slices.Contains(categories, 3) || slices.Contains(categories, 100003)
}

func antTVScope(item map[string]any, files []string, fileCount int) *api.DupeTVScope {
	season, known := antNonNegativeInt(item["season"])
	var seasonNumber *int
	if known {
		seasonNumber = new(season)
	}
	if antSeasonCategory(item) {
		// The API's Season category also holds partial batches. Only complete
		// per-file evidence can bound their membership; the category cannot.
		return dupe.NumberedTVScopeFromFiles(files, fileCount, seasonNumber)
	}
	if !known {
		return nil
	}
	scope := &api.DupeTVScope{Season: seasonNumber}
	if episode, present := antNonNegativeInt(item["episode"]); present && episode > 0 {
		scope.Episodes = []int{episode}
	}
	return scope
}

func antProviderIDs(item map[string]any) []api.TrackerProviderID {
	ids := make([]api.TrackerProviderID, 0, 3)
	for _, value := range []struct {
		provider string
		id       int
	}{
		{"imdb", antIMDB(item["imdb"])},
		{"tmdb", int(antInt(item["tmdb"]))},
		{"tvdb", int(antInt(item["tvdbid"]))},
	} {
		if value.id > 0 {
			ids = append(ids, api.TrackerProviderID{Provider: value.provider, Value: strconv.Itoa(value.id)})
		}
	}
	return ids
}

func filterANTWork(entries []api.DupeEntry, identity api.ExternalIdentity) []api.DupeEntry {
	return slices.DeleteFunc(entries, func(entry api.DupeEntry) bool {
		if entry.Category != "" && identity.Category != "" && entry.Category != string(identity.Category) {
			return true
		}
		for _, id := range entry.ProviderIDs {
			wanted := 0
			switch id.Provider {
			case "imdb":
				wanted = identity.IMDBID
			case "tmdb":
				wanted = identity.TMDBID
			case "tvdb":
				wanted = identity.TVDBID
			}
			if wanted > 0 && id.Value != strconv.Itoa(wanted) {
				return true
			}
		}
		return false
	})
}
